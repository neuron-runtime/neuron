package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/api"
	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	"github.com/neuron-runtime/neuron/nore/internal/execution/engine"
	"github.com/neuron-runtime/neuron/nore/internal/instance"
	"github.com/neuron-runtime/neuron/nore/internal/invoke"
	"github.com/neuron-runtime/neuron/nore/internal/planner"
	"github.com/neuron-runtime/neuron/nore/internal/plugin"
	"github.com/neuron-runtime/neuron/nore/internal/resolver"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	_ "github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	_ "github.com/neuron-runtime/neuron/nore/internal/storage/postgres"
	"github.com/neuron-runtime/neuron/shared/types/apitoken"
	"github.com/neuron-runtime/neuron/shared/types/builtins"
	"github.com/neuron-runtime/neuron/shared/types/projectruntime"
	"github.com/neuron-runtime/neuron/shared/version"
)

// capabilityRuntimeShutdownTimeout bounds the final close of the capability
// runtime backends, so a backend that hangs on shutdown cannot keep the daemon
// alive indefinitely.
const capabilityRuntimeShutdownTimeout = 10 * time.Second

func main() {
	var (
		port                 string
		socket               string
		workers              int
		dataDir              string
		storageDriver        string
		storagePath          string
		storageDSN           string
		tokenArg             string
		detachedDrainTimeout time.Duration
		capabilityStore      string
		showVer              bool
		invokeMode           bool
		projectMode          bool
		projectRoot          string
		host                 string
		builtinPolicyArg     string
	)

	flag.StringVar(&port, "port", "", "TCP address for the N.O.R.E. API; empty disables TCP (default: Unix socket only)")
	flag.StringVar(&socket, "socket", defaultSocket(), "Unix socket for local CLI clients; empty disables Unix socket")
	flag.IntVar(&workers, "workers", 8, "capability runtime worker count")
	flag.StringVar(&dataDir, "data-dir", defaultDataDir(), "persistent data directory")
	flag.StringVar(&storageDriver, "storage-driver", defaultStorageDriver(), "storage backend driver (default: sqlite)")
	flag.StringVar(&storagePath, "storage-path", "", "storage path override (sqlite)")
	flag.StringVar(&storageDSN, "storage-dsn", os.Getenv("NEURON_DATABASE_URL"), "storage DSN (postgres)")
	flag.StringVar(&tokenArg, "token", "", "API token for authenticating requests; empty loads the token from the socket's token file")
	flag.DurationVar(&detachedDrainTimeout, "detached-drain-timeout", engine.DefaultDetachedDrainTimeout, "how long detached capability work may run after the daemon begins shutting down")
	flag.StringVar(&capabilityStore, "capability-runtime-store", defaultCapabilityRuntimeStore(), "capability runtime store directory for resolving installed artifacts")
	flag.BoolVar(&showVer, "version", false, "print the N.O.R.E. version and exit")
	flag.BoolVar(&invokeMode, "invoke", false, "one-shot direct invocation: read an InvokeRequest from stdin and write an InvokeResponse to stdout")
	flag.BoolVar(&projectMode, "project", false, "run in project runtime mode (foreground, loopback TCP, auto port)")
	flag.StringVar(&projectRoot, "project-root", "", "project root directory (required with --project)")
	flag.StringVar(&host, "host", "127.0.0.1", "host interface to bind to in project mode (default: 127.0.0.1)")
	flag.StringVar(&builtinPolicyArg, "builtin-policy", "", "JSON policy bounding the built-in capability runtimes (neuron:core:*)")
	flag.Parse()

	if envDriver := os.Getenv("NEURON_STORAGE_DRIVER"); envDriver != "" {
		storageDriver = envDriver
	}
	if envDSN := os.Getenv("NEURON_DATABASE_URL"); envDSN != "" {
		storageDSN = envDSN
	}

	if showVer {
		fmt.Println(version.String())
		return
	}

	// The built-in capability policy is resolved once, at the process boundary:
	// the policy may arrive from a project configuration over the command line,
	// and it is resolved against the ceilings compiled into this binary so a
	// policy that arrived from outside cannot widen what a capability reaches.
	builtinPolicy, err := resolveBuiltinPolicy(builtinPolicyArg)
	if err != nil {
		log.Fatalf("resolve built-in capability policy: %v", err)
	}

	// Direct invocation mode: host a single installed Capability Runtime for one
	// execution and exit. No storage, assemblies, or API are involved.
	if invokeMode {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := invoke.Run(ctx, capabilityStore, builtinPolicy, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	// Project mode: run as a foreground project runtime with auto port allocation
	// and discovery metadata.
	if projectMode {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		runProjectMode(ctx, host, port, projectRoot, workers, dataDir, storageDriver, storagePath, storageDSN, capabilityStore, detachedDrainTimeout, tokenArg, builtinPolicy)
		return
	}

	// Traditional daemon mode (global Unix socket or explicit TCP)
	// A TCP listener is reachable by anything that can route to this host, so it
	// is refused outright unless an API token is in force. Serving the assembly
	// registration, instance, and execution API without a credential over TCP
	// would let a remote caller run arbitrary capability runtimes.
	token, generatedTokenFile, err := resolveAPIToken(tokenArg, socket)
	if err != nil {
		log.Fatalf("resolve api token: %v", err)
	}
	if port != "" && token == "" {
		// Name only the sources resolveAPIToken actually reads. NEURON_API_TOKEN
		// authenticates clients and is never consulted by the daemon, so telling
		// an operator to set it here would send them down a path that cannot work.
		log.Fatal("refusing to listen on TCP " + port + " without an API token; pass --token, or point NEURON_API_TOKEN_FILE at a file holding one")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	plugin.SetStoreRoot(capabilityStore)

	storePath := dataDir
	if storagePath != "" {
		storePath = storagePath
	}
	if storageDriver == "" || storageDriver == "sqlite" {
		if err := os.MkdirAll(storePath, 0o755); err != nil {
			log.Fatalf("create data directory %s: %v", storePath, err)
		}
	}

	store, err := storage.Open(ctx, storage.Config{Driver: storageDriver, DataDir: storePath, Path: storePath, DSN: storageDSN})
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

	inst, err := instance.NewManager(ctx, workers, detachedDrainTimeout, store, assemblies,
		instance.WithBuiltinPolicy(builtinPolicy))
	if err != nil {
		log.Fatalf("init instance manager: %v", err)
	}
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
		fmt.Printf("Neuron runtime listening on %s\n", port)
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
		fmt.Printf("Neuron runtime socket: %s\n", socket)
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
		shutdownRuntimes(srv)
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
		shutdownRuntimes(srv)
		log.Fatal(err)
	}
}

// shutdownRuntimes stops the live instances and then releases the capability
// runtime backends this process was hosting.
//
// The order matters. Stopping an instance is where it gives up the backend
// instance it used, so doing it first lets the backends release normally rather
// than being torn down from under a running execution. Closing the backends
// afterwards is what cleans up the state that has no per-instance handle at all:
// worker pools an instance failed to release, and the WASM backend's
// compiled-module cache and sandbox runtime.
//
// The context is already cancelled, so the close needs a deadline of its own to
// finish rather than being abandoned at the first cancellation.
func shutdownRuntimes(srv *api.Server) {
	srv.StopInstances()

	ctx, cancel := context.WithTimeout(context.Background(), capabilityRuntimeShutdownTimeout)
	defer cancel()
	if err := plugin.CloseSharedRuntimes(ctx); err != nil {
		slog.Warn("close capability runtime backends", slog.String("error", err.Error()))
	}
}

// resolveBuiltinPolicy decodes the built-in capability policy the runtime was
// started with.
//
// The policy travels as JSON on the command line because the runtime is a
// separate process from the CLI. It is resolved against the ceilings compiled
// into this binary, so a policy that arrives over a transport it did not come
// from cannot widen what a capability may reach, and it is validated so a
// policy that cannot be applied stops the runtime instead of being ignored.
func resolveBuiltinPolicy(argument string) (builtins.Policy, error) {
	var policy builtins.Policy
	if strings.TrimSpace(argument) != "" {
		if err := json.Unmarshal([]byte(argument), &policy); err != nil {
			return builtins.Policy{}, fmt.Errorf("decode the built-in capability policy: %w", err)
		}
	}

	effective, err := builtins.Effective(policy)
	if err != nil {
		return builtins.Policy{}, err
	}
	if err := effective.Validate(); err != nil {
		return builtins.Policy{}, err
	}
	return effective, nil
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

func defaultStorageDriver() string {
	if value := os.Getenv("NEURON_STORAGE_PROVIDER"); value != "" {
		return value
	}
	return storage.DefaultDriver
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

func defaultCapabilityRuntimeStore() string {
	if value := os.Getenv("NEURON_CAPABILITY_RUNTIME_STORE"); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".neuron/capabilityRuntimes"
	}
	return filepath.Join(home, ".neuron", "capabilityRuntimes")
}

// runProjectMode runs N.O.R.E. as a foreground project runtime with auto port
// allocation and discovery metadata.
func runProjectMode(ctx context.Context, host, port, projectRoot string, workers int, dataDir, storageDriver, storagePath, storageDSN, capabilityStore string, detachedDrainTimeout time.Duration, tokenArg string, builtinPolicy builtins.Policy) {
	if projectRoot == "" {
		log.Fatal("--project-root is required when --project is set")
	}

	// Resolve the absolute project root
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		log.Fatalf("resolve project root: %v", err)
	}

	// The project root is the root the built-in capability runtimes are
	// confined to. The runtime knows the project it was started for, so it
	// states the root itself rather than trusting the one that arrived with
	// the policy.
	builtinPolicy.ProjectRoot = absRoot

	// Generate an API token for this project runtime
	token, err := generateProjectToken()
	if err != nil {
		log.Fatalf("generate api token: %v", err)
	}

	// Bind the runtime's listener. An empty port asks the OS for an available
	// one; the bound port is what gets published, never the requested one.
	listener, err := listenProject(host, port)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	_, portStr, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		log.Fatalf("parse bound address: %v", err)
	}

	// Create data directory
	storePath := dataDir
	if storagePath != "" {
		storePath = storagePath
	}
	if storageDriver == "" || storageDriver == "sqlite" {
		if err := os.MkdirAll(storePath, 0o755); err != nil {
			log.Fatalf("create data directory %s: %v", storePath, err)
		}
	}

	// Open storage before publishing the discovery record. A runtime that
	// cannot initialize its durable state must not advertise itself as ready.
	store, err := storage.Open(ctx, storage.Config{Driver: storageDriver, DataDir: storePath, Path: storePath, DSN: storageDSN})
	if err != nil {
		log.Fatalf("init storage: %v", err)
	}
	defer store.Close()

	// Publish the discovery record so `neuron run` can find this runtime. The
	// record is owner-only: it carries the API token. Each session writes its
	// own record file, so concurrent sessions for the same project do not
	// overwrite one another.
	session := projectruntime.New(host, portStr, absRoot, os.Getpid(), token)
	if err := projectruntime.Write(absRoot, session); err != nil {
		log.Fatalf("write discovery record: %v", err)
	}

	// Print the canonical URL for the user
	fmt.Printf("Neuron project runtime started on http://%s:%s\n", host, portStr)
	fmt.Printf("Project root: %s\n", absRoot)
	fmt.Printf("Press Ctrl-C to stop\n")

	plugin.SetStoreRoot(capabilityStore)

	assemblies := assembly.NewRepository(store)

	celCompiler, err := resolver.NewCELCompiler(resolver.DefaultCELConfig())
	if err != nil {
		log.Fatalf("init cel compiler: %v", err)
	}
	compiler, err := planner.NewCompiler(celCompiler)
	if err != nil {
		log.Fatalf("init planner: %v", err)
	}

	inst, err := instance.NewManager(ctx, workers, detachedDrainTimeout, store, assemblies,
		instance.WithBuiltinPolicy(builtinPolicy))
	if err != nil {
		log.Fatalf("init instance manager: %v", err)
	}
	srv := api.NewServer(inst, assemblies, compiler, token)

	// Serve on the listener
	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ctx, listener); err != nil && ctx.Err() == nil {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownRuntimes(srv)
		_ = projectruntime.Remove(absRoot, session.SessionID)
	case err := <-errCh:
		shutdownRuntimes(srv)
		_ = projectruntime.Remove(absRoot, session.SessionID)
		log.Fatal(err)
	}
}

// listenProject binds the project runtime's TCP listener. An empty port asks
// the OS for a free port and retaining that bound listener is what makes
// concurrent projects safe: the OS resolves the race instead of the process
// probing for a port and then racing to claim it.
func listenProject(host, port string) (net.Listener, error) {
	addr := host + ":" + port
	if port == "" {
		addr = host + ":0"
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	return listener, nil
}

// generateProjectToken generates a secure API token for a project runtime.
func generateProjectToken() (string, error) {
	token, err := apitoken.Generate()
	if err != nil {
		return "", err
	}
	return token, nil
}
