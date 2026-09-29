package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/neuron-runtime/neuron/nore/internal/api"
	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	"github.com/neuron-runtime/neuron/nore/internal/instance"
	"github.com/neuron-runtime/neuron/nore/internal/planner"
	"github.com/neuron-runtime/neuron/nore/internal/resolver"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	"github.com/neuron-runtime/neuron/shared/types/apitoken"
	"github.com/neuron-runtime/neuron/shared/version"
)

func main() {
	var (
		port     string
		socket   string
		workers  int
		dataDir  string
		tokenArg string
		showVer  bool
	)

	flag.StringVar(&port, "port", "", "TCP address for the N.O.R.E. API; empty disables TCP (default: Unix socket only)")
	flag.StringVar(&socket, "socket", defaultSocket(), "Unix socket for local CLI clients; empty disables Unix socket")
	flag.IntVar(&workers, "workers", 8, "capability runtime worker count")
	flag.StringVar(&dataDir, "data-dir", defaultDataDir(), "persistent data directory")
	flag.StringVar(&tokenArg, "token", "", "API token for authenticating requests; empty loads the token from the socket's token file")
	flag.BoolVar(&showVer, "version", false, "print the N.O.R.E. version and exit")
	flag.Parse()

	if showVer {
		fmt.Println(version.String())
		return
	}

	// A TCP listener is reachable by anything that can route to this host, so it
	// is refused outright unless an API token is in force. Serving the assembly
	// registration, instance, and execution API without a credential over TCP
	// would let a remote caller run arbitrary capability runtimes.
	token, generatedTokenFile, err := resolveAPIToken(tokenArg, socket)
	if err != nil {
		log.Fatalf("resolve api token: %v", err)
	}
	if port != "" && token == "" {
		log.Fatal("refusing to listen on TCP " + port + " without an API token; pass --token, set NEURON_API_TOKEN, or configure a token file")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("create data directory %s: %v", dataDir, err)
	}

	store, err := sqlite.New(storage.Config{DataDir: dataDir})
	if err != nil {
		log.Fatalf("init storage: %v", err)
	}
	defer store.Close()

	assemblies := assembly.NewRepository(store)

	celCompiler, err := resolver.NewCELCompiler(resolver.DefaultCELConfig())
	if err != nil {
		log.Fatalf("init cel compiler: %v", err)
	}
	compiler, err := planner.NewCompiler(celCompiler)
	if err != nil {
		log.Fatalf("init planner: %v", err)
	}

	inst := instance.NewManager(ctx, workers, store, assemblies)
	srv := api.NewServer(inst, assemblies, compiler, token)

	type listenerEntry struct {
		name string
		l    net.Listener
	}

	var listeners []listenerEntry

	if port != "" {
		l, err := net.Listen("tcp", port)
		if err != nil {
			log.Fatalf("listen on %s: %v", port, err)
		}
		listeners = append(listeners, listenerEntry{"tcp", l})
		fmt.Printf("N.O.R.E. listening on %s\n", port)
	}

	if socket != "" {
		// The socket directory holds the API token, so it is owner-only. A
		// token readable by other local users would provide no protection at
		// all, regardless of the socket's own permissions.
		if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
			log.Fatalf("create socket directory: %v", err)
		}
		// A socket file outlives a crashed daemon and would make the next start
		// fail with "address already in use", so a leftover is cleared first.
		if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
			log.Fatalf("remove stale socket %s: %v", socket, err)
		}

		l, err := net.Listen("unix", socket)
		if err != nil {
			log.Fatalf("listen on unix socket %s: %v", socket, err)
		}
		if err := os.Chmod(socket, 0o600); err != nil {
			log.Fatalf("secure unix socket %s: %v", socket, err)
		}
		listeners = append(listeners, listenerEntry{"unix", l})
		fmt.Printf("N.O.R.E. local socket: %s\n", socket)
	}

	if len(listeners) == 0 {
		log.Fatal("at least one of --port or --socket must be configured")
	}

	errCh := make(chan error, len(listeners))
	for _, entry := range listeners {
		go func() {
			if err := srv.Serve(ctx, entry.l); err != nil && ctx.Err() == nil {
				errCh <- fmt.Errorf("%s server: %w", entry.name, err)
			}
		}()
	}

	select {
	case <-ctx.Done():
		for _, entry := range listeners {
			_ = entry.l.Close()
		}
		// Gracefully stop all live instances so capability runtime-backed resources
		// (worker processes and WASM modules) receive a clean shutdown instead
		// of being torn down by process exit.
		srv.StopInstances()
		// Close the local endpoints so the next daemon start finds no socket
		// file and no credential left behind by a process that no longer exists.
		if socket != "" {
			_ = os.Remove(socket)
		}
		if generatedTokenFile {
			_ = os.Remove(tokenPathFor(socket))
		}
	case err := <-errCh:
		for _, entry := range listeners {
			_ = entry.l.Close()
		}
		log.Fatal(err)
	}
}

// resolveAPIToken determines the token the API will require.
//
// The token is taken from the flag first, then from the token file beside the
// socket, and is generated when neither is present. Generating a token is
// preferable to running unauthenticated: it is the only way a socket-scoped
// daemon can protect the ability to run capability runtimes from other local
// processes without asking the operator to manage a secret. The returned
// boolean reports whether the token file was created by this call, so shutdown
// removes only a credential this process owns.
func resolveAPIToken(tokenArg, socket string) (string, bool, error) {
	if tokenArg != "" {
		return tokenArg, false, nil
	}
	if socket == "" {
		// TCP-only with no token: the caller refuses to start.
		return "", false, nil
	}

	path := tokenPathFor(socket)
	if existing, ok := apitoken.Read(path); ok {
		return existing, false, nil
	}

	generated, err := apitoken.Generate()
	if err != nil {
		return "", false, err
	}
	if err := apitoken.Write(path, generated); err != nil {
		return "", false, err
	}
	return generated, true, nil
}

// tokenPathFor returns the token file path for a socket, honoring an explicit
// override so an operator can keep the credential outside the socket directory.
func tokenPathFor(socket string) string {
	if override := os.Getenv(apitoken.EnvTokenFile); override != "" {
		return override
	}
	return apitoken.TokenFilePath(socket)
}

func defaultSocket() string {
	if value := os.Getenv("NEURON_SOCKET"); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/neuron/nore.sock"
	}
	return filepath.Join(home, ".neuron", "nore.sock")
}

func defaultDataDir() string {
	if value := os.Getenv("NEURON_DATA_DIR"); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/neuron/data"
	}
	return filepath.Join(home, ".neuron", "nore")
}
