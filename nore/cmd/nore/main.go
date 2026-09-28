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

	"github.com/Muhammad-Jay/neuron/nore/internal/api"
	"github.com/Muhammad-Jay/neuron/nore/internal/instance"
	"github.com/Muhammad-Jay/neuron/nore/internal/planner"
	"github.com/Muhammad-Jay/neuron/nore/internal/resolver"
	"github.com/Muhammad-Jay/neuron/nore/internal/storage"
	"github.com/Muhammad-Jay/neuron/nore/internal/storage/sqlite"
	"github.com/Muhammad-Jay/neuron/nore/internal/assembly"
	"github.com/Muhammad-Jay/neuron/shared/version"
)

func main() {
	var (
		port    string
		socket  string
		workers int
		dataDir string
		showVer bool
	)

	flag.StringVar(&port, "port", "", "TCP address for the N.O.R.E. API; empty disables TCP (default: Unix socket only)")
	flag.StringVar(&socket, "socket", defaultSocket(), "Unix socket for local CLI clients; empty disables Unix socket")
	flag.IntVar(&workers, "workers", 8, "capability runtime worker count")
	flag.StringVar(&dataDir, "data-dir", defaultDataDir(), "persistent data directory")
	flag.BoolVar(&showVer, "version", false, "print the N.O.R.E. version and exit")
	flag.Parse()

	if showVer {
		fmt.Println(version.String())
		return
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
	srv := api.NewServer(inst, assemblies, compiler)

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
		if err := os.MkdirAll(filepath.Dir(socket), 0o755); err != nil {
			log.Fatalf("create socket directory: %v", err)
		}
		_ = os.Remove(socket)

		l, err := net.Listen("unix", socket)
		if err != nil {
			log.Fatalf("listen on unix socket %s: %v", socket, err)
		}
		_ = os.Chmod(socket, 0o600)
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
	case err := <-errCh:
		for _, entry := range listeners {
			_ = entry.l.Close()
		}
		log.Fatal(err)
	}
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
