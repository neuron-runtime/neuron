package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	goruntime "runtime"

	"github.com/Muhammad-Jay/neuron/application/client"
	"github.com/Muhammad-Jay/neuron/application/config"
	"github.com/Muhammad-Jay/neuron/application/connection"
	"github.com/Muhammad-Jay/neuron/application/daemon"
	"github.com/Muhammad-Jay/neuron/application/runtime"
)

// Options carries everything SetupClient needs. It is filled in by the CLI
// layer so bootstrap never has to read configuration or flags itself.
type Options struct {
	Config config.Config

	// Verbose controls whether a spawned local daemon attaches its output.
	Verbose bool
}

// SetupClient initializes the connection, ensures the daemon is running
// (if local), and returns the initialized client alongside a cleanup function.
func SetupClient(ctx context.Context, opts Options) (*client.Client, func(), error) {
	cfg := opts.Config

	var conn connection.Connection

	if cfg.Daemon.Endpoint != "" {
		// Use HTTP for remote execution (No daemon needed)
		conn = connection.New(connection.NewHTTPTransport(nil, cfg.Daemon.Endpoint))
	} else {
		// Use Unix Socket for local execution
		conn = connection.New(connection.NewLocal(cfg.Daemon.Socket))
	}

	dCfg := daemon.ConfigFromEffective(cfg)

	// MAGIC: AttachOutput controls whether the daemon prints to the console!
	// If false, the daemon runs silently in the background.
	dCfg.AttachOutput = opts.Verbose

	// The daemon binary path is resolved lazily by the runtime manager: it is
	// needed only when a local daemon must be started. A daemon already running
	// (started by a previous command) is reused as-is, so a source-tree checkout
	// without --nore-path still works as long as the daemon is healthy.
	rtManager := runtime.NewManager(dCfg, conn, func() (string, error) { return NoreBinaryPath(cfg) })

	// Ensure N.O.R.E is running & accessible
	// If daemon.Endpoint is set, we simply ping the remote server and skip the daemon!
	if err := rtManager.Ensure(ctx, cfg.Daemon.Endpoint != ""); err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("failed to ensure N.O.R.E runtime: %w", err)
	}

	// Create the Client SDK to return to the CLI command
	c := client.New(conn)

	cleanup := func() {
		_ = c.Close()
	}

	return c, cleanup, nil
}

// NoreBinaryPath resolves the nore binary, honoring an explicit config path
// first, then the bundled binary that ships next to the neuron executable, then
// the system PATH, and finally a local development fallback.
//
// Resolution order:
//
//  1. daemon.norePath (config file, environment, or --nore-path). Explicit
//     override, always honored.
//  2. The bundled release binary: the product archive ships `neuron` and `nore`
//     side by side, so the CLI prefers a `nore` executable in its own directory
//     over anything on PATH.
//  3. `nore` on the system PATH (globally installed runtime).
//  4. Development fallback for a local repository checkout.
func NoreBinaryPath(cfg config.Config) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		// Degrade gracefully: PATH and the checkout fallback remain usable.
		exe = ""
	}
	return resolveNoreBinary(cfg.Daemon.NorePath, exe)
}

func resolveNoreBinary(custom, exe string) (string, error) {
	if custom != "" {
		return custom, nil
	}

	if exe != "" {
		sibling := filepath.Join(filepath.Dir(exe), binaryName("nore"))
		if isRunnable(sibling) {
			return sibling, nil
		}
	}

	if path, err := exec.LookPath("nore"); err == nil {
		return path, nil
	}

	if cwd, err := os.Getwd(); err == nil {
		for _, candidate := range []string{
			filepath.Join(cwd, "../../../nore/cmd/nore/nore-daemon"),
			filepath.Join(cwd, "../../../nore/cmd/nore/nore"),
			filepath.Join(cwd, "../../../nore/cmd/nore/nore.exe"),
			filepath.Join(cwd, "nore/cmd/nore/nore-daemon"),
			filepath.Join(cwd, "nore/cmd/nore/nore"),
		} {
			if isRunnable(candidate) {
				return candidate, nil
			}
		}
	}

	return "", errors.New(
		"nore runtime not found: set daemon.norePath (or --nore-path) to the N.O.R.E. daemon binary",
	)
}

// binaryName returns the platform-specific executable name.
func binaryName(name string) string {
	if goruntime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// isRunnable reports whether the path exists and is executable. On Windows the
// executable bit is unreliable, so existence is sufficient there.
func isRunnable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return goruntime.GOOS == "windows" || info.Mode()&0o111 != 0
}
