package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/neuron-runtime/neuron/application/client"
	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/application/connection"
	"github.com/neuron-runtime/neuron/shared/types/projectruntime"
)

// ErrNoProjectRuntime reports that no live project runtime is published for the
// project. Instance commands surface it instead of silently falling back to the
// build daemon, which would return empty results for executions the project
// runtime owns.
var ErrNoProjectRuntime = errors.New("no project runtime found; start one with `neuron` in the project directory")

// ProjectRoot returns the project root the effective configuration was loaded
// for. The loader resolves it from the nearest ancestor holding a neuron.config.*
// file (falling back to the working directory), so a command run from a
// subdirectory still addresses the project that owns it.
func ProjectRoot(cfg config.Config) (string, error) {
	if cfg.ProjectDir != "" {
		return cfg.ProjectDir, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get current directory: %w", err)
	}
	return cwd, nil
}

// DiscoverProjectRuntime resolves the live project runtime for the project the
// effective configuration belongs to. It returns the runtime endpoint and the
// API token its discovery record publishes.
//
// An explicit port overrides endpoint discovery but still adopts the token if
// the project runtime published one, so an operator pointing at a known port is
// not locked out by authentication. Discovery walks up from the project root, so
// a runtime started at any ancestor is found. A missing runtime is an explicit
// error rather than an implicit choice of a different runtime.
func DiscoverProjectRuntime(cfg config.Config, overridePort string) (endpoint, token string, err error) {
	root, err := ProjectRoot(cfg)
	if err != nil {
		return "", "", fmt.Errorf("discover local runtime: %w", err)
	}

	if overridePort != "" {
		host := cfg.Runtime.Host
		if host == "" {
			host = "127.0.0.1"
		}
		// Best effort: an explicit port is authoritative even if no discovery
		// record exists next to it.
		if record, found, err := findRuntimeRecord(root); err == nil && found {
			token = record.Token
		}
		return fmt.Sprintf("http://%s:%s", host, overridePort), token, nil
	}

	record, found, err := findRuntimeRecord(root)
	if err != nil {
		return "", "", fmt.Errorf("discover local runtime: %w", err)
	}
	if !found {
		return "", "", ErrNoProjectRuntime
	}
	return record.Endpoint(), record.Token, nil
}

// NewProjectRuntimeClient builds an HTTP client for a project runtime endpoint,
// presenting the token from its discovery record when it published one. It is
// the single token-adoption path shared by assembly execution and instance
// commands; the credential stays in a request header and is never surfaced in
// errors or diagnostics.
func NewProjectRuntimeClient(endpoint, token string) *client.Client {
	conn := connection.NewRemote(endpoint)
	if token != "" {
		if transport, ok := conn.Transport().(*connection.HTTPTransport); ok {
			transport.WithToken(token)
		}
	}
	return client.New(conn)
}

// SetupInstanceClient returns the client for instance-domain commands.
//
// An explicitly configured endpoint (--remote or a daemon endpoint) is always
// honored through SetupClient, which preserves the remote and authenticated
// socket workflows. Otherwise the live project runtime record is used, because
// executions live in the project runtime rather than the build daemon. There is
// deliberately no socket fallback: querying the wrong runtime reports empty
// instance lists instead of telling the operator that no runtime is running.
func SetupInstanceClient(ctx context.Context, opts Options) (*client.Client, func(), error) {
	cfg := opts.Config
	if cfg.Daemon.Endpoint != "" {
		return SetupClient(ctx, opts)
	}

	endpoint, token, err := DiscoverProjectRuntime(cfg, "")
	if err != nil {
		return nil, nil, err
	}
	c := NewProjectRuntimeClient(endpoint, token)
	return c, func() { _ = c.Close() }, nil
}

// findRuntimeRecord walks up from startDir looking for a project runtime
// discovery record. It returns the newest live record for the nearest project
// that has one. A record left behind by a dead or foreign process is reported as
// an error rather than silently ignored, because it means the project believes a
// runtime is running when it is not.
func findRuntimeRecord(startDir string) (projectruntime.Record, bool, error) {
	root := filepath.Clean(startDir)
	for {
		record, err := selectRuntimeRecord(root)
		if err != nil {
			return projectruntime.Record{}, false, err
		}
		if record != nil {
			return *record, true, nil
		}
		parent := filepath.Dir(root)
		if parent == root {
			return projectruntime.Record{}, false, nil // reached the filesystem root
		}
		root = parent
	}
}

// selectRuntimeRecord returns the newest live runtime record for a project root,
// or nil when the project has no runtime record at all. Several sessions may
// publish records for the same project; the newest whose process is still alive
// and whose project root matches wins. If records exist but none is usable, the
// newest unusable record is reported as an error so the caller learns the
// project's runtime is stale rather than absent.
func selectRuntimeRecord(root string) (*projectruntime.Record, error) {
	records, err := projectruntime.ReadAll(root)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	for i := range records {
		if err := validateRuntimeRecord(records[i], root); err == nil {
			return &records[i], nil
		}
	}
	return nil, validateRuntimeRecord(records[0], root)
}

// validateRuntimeRecord rejects a record that cannot belong to a live runtime
// for this project.
func validateRuntimeRecord(record projectruntime.Record, root string) error {
	if record.PID <= 0 {
		return nil
	}
	if !isProcessAlive(record.PID) {
		return fmt.Errorf("runtime process %d is not alive", record.PID)
	}
	if record.ProjectRoot != "" && record.ProjectRoot != root {
		return fmt.Errorf("discovery record project root mismatch")
	}
	return nil
}

func isProcessAlive(pid int) bool {
	// On Unix, signal 0 checks if process exists without sending a signal
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 doesn't actually send a signal, just checks existence
	return proc.Signal(syscall.Signal(0)) == nil
}
