// Package process implements a Runtime that hosts executors as OS child
// processes.
//
// Two transports are supported, selected by the executor's declared protocol:
//
//   - neuron/executor-v1 (canonical): the executor process speaks gRPC
//     over a Unix domain socket (see packages/executor-sdks/golang). The runtime
//     maintains a pool of long-lived worker processes, leasing requests to
//     available workers. This provides warm runtimes, connection reuse,
//     bounded concurrency, health checking, and graceful shutdown.
//   - neuron/executor-v1-json (legacy): the executor speaks the one-shot
//     stdin/stdout JSON protocol. Each execution spawns a fresh process,
//     feeds it the JSON request on stdin, and reads the JSON response from
//     stdout. This is the transport used by WASI executors and by process
//     executors that predate the gRPC protocol.
//
// The protocol negotiation happens during the Initialize handshake for gRPC
// workers: the runtime sends the protocol version and executor identity, and
// the executor responds with its supported version and capabilities.
package process

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	shadexec "github.com/Muhammad-Jay/neuron/shared/types/executor"
)

const (
	// defaultMaxWorkers is the default maximum number of concurrent gRPC
	// worker processes per executor type when the manifest does not specify
	// maxWorkers.
	defaultMaxWorkers = 1

	// defaultShutdownTimeout is the maximum time to wait for graceful
	// shutdown before forcefully killing a worker process.
	defaultShutdownTimeout = 10 * time.Second

	// defaultStartTimeout is the maximum time to wait for a worker to
	// signal readiness and complete the Initialize handshake.
	defaultStartTimeout = 30 * time.Second
)

// Runtime hosts executor processes and manages their lifecycle. It implements
// shadexec.Runtime and is registered with the runtime registry for the
// "process" kind.
type Runtime struct {
	mu     sync.RWMutex
	pools  map[string]*workerPool
	logger *slog.Logger
}

// New returns a Process Runtime ready to manage executor workers.
func New(logger *slog.Logger) *Runtime {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runtime{
		pools:  make(map[string]*workerPool),
		logger: logger,
	}
}

// RuntimeName returns "process", matching RuntimeKindProcess.
func (r *Runtime) RuntimeName() string {
	return shadexec.RuntimeKindProcess
}

// Start launches an executor instance backed by a pool of long-lived
// worker processes. The pool is created lazily on the first request.
func (r *Runtime) Start(ctx context.Context, spec shadexec.StartSpec) (shadexec.Instance, error) {
	if spec.Entrypoint == "" {
		return nil, fmt.Errorf("executor %s: no entrypoint", spec.Type)
	}
	if _, err := os.Stat(spec.Entrypoint); err != nil {
		return nil, fmt.Errorf("executor %s: entrypoint %s: %w", spec.Type, spec.Entrypoint, err)
	}

	protocol := spec.Protocol
	if protocol == "" {
		protocol = shadexec.ProtocolV1
	}

	switch protocol {
	case shadexec.ProtocolJSONV1:
		// Legacy stdin/stdout JSON transport: each execution spawns a fresh
		// process. This is the transport used by WASI executors and by
		// process executors that predate the gRPC protocol.
		return newLegacyInstance(spec, r.logger)
	case shadexec.ProtocolV1:
		// Canonical gRPC transport over Unix domain sockets with a pool of
		// long-lived worker processes.
		maxWorkers := spec.MaxWorkers
		if maxWorkers <= 0 {
			maxWorkers = defaultMaxWorkers
		}

		pool := &workerPool{
			type_:      spec.Type,
			version:    spec.Version,
			entrypoint: spec.Entrypoint,
			protocol:   protocol,
			maxWorkers: maxWorkers,
			rootDir:    spec.RootDir,
			logger:     r.logger.With("executor", spec.Type, "version", spec.Version),
		}

		key := instanceKey(spec.Type, spec.Version)
		r.mu.Lock()
		if existing, ok := r.pools[key]; ok {
			existing.Close(ctx)
		}
		r.pools[key] = pool
		r.mu.Unlock()

		return pool, nil
	default:
		return nil, fmt.Errorf("executor %s: unsupported protocol %q (supported: %q, %q)",
			spec.Type, protocol, shadexec.ProtocolV1, shadexec.ProtocolJSONV1)
	}
}

// Close shuts down all pools managed by this runtime.
func (r *Runtime) Close(ctx context.Context) error {
	r.mu.Lock()
	pools := make([]*workerPool, 0, len(r.pools))
	for _, p := range r.pools {
		pools = append(pools, p)
	}
	r.pools = make(map[string]*workerPool)
	r.mu.Unlock()

	var errs []error
	for _, p := range pools {
		if err := p.Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("close process runtime: %v", errs)
	}
	return nil
}

func instanceKey(typ, version string) string {
	return typ + "@" + version
}

// resolveEntrypoint returns the absolute entrypoint for a frozen executor,
// handling both manifest-relative and absolute paths.
func resolveEntrypoint(rootDir, entrypoint string) string {
	if filepath.IsAbs(entrypoint) {
		return entrypoint
	}
	return filepath.Join(rootDir, filepath.FromSlash(entrypoint))
}
