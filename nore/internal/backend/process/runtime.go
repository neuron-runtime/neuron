// Package process implements a Backend that hosts capability runtimes as OS child
// processes.
//
// Two transports are supported, selected by the capability runtime's declared protocol:
//
//   - neuron/capability-runtime-v1 (canonical): the capability runtime process speaks gRPC
//     over a Unix domain socket (see packages/capability runtime-sdks/golang). The runtime
//     maintains a pool of long-lived worker processes, leasing requests to
//     available workers. This provides warm runtimes, connection reuse,
//     bounded concurrency, health checking, and graceful shutdown.
//   - neuron/capability-runtime-v1-json (legacy): the capability runtime speaks the one-shot
//     stdin/stdout JSON protocol. Each execution spawns a fresh process,
//     feeds it the JSON request on stdin, and reads the JSON response from
//     stdout. This is the transport used by WASI capability runtimes and by process
//     capability runtimes that predate the gRPC protocol.
//
// The protocol negotiation happens during the Initialize handshake for gRPC
// workers: the runtime sends the protocol version and capability runtime identity, and
// the capability runtime responds with its supported version and capabilities.
package process

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

const (
	// defaultMaxWorkers is the default maximum number of concurrent gRPC
	// worker processes per capability runtime type when the manifest does not specify
	// maxWorkers.
	defaultMaxWorkers = 1

	// defaultShutdownTimeout is the maximum time to wait for graceful
	// shutdown before forcefully killing a worker process.
	defaultShutdownTimeout = 10 * time.Second

	// defaultStartTimeout is the maximum time to wait for a worker to
	// signal readiness and complete the Initialize handshake.
	defaultStartTimeout = 30 * time.Second
)

// Backend hosts capability runtime processes and manages their lifecycle. It implements
// capabilityrt.Backend and is registered with the backend registry for the
// "process" kind.
type Backend struct {
	mu     sync.RWMutex
	pools  map[string]*workerPool
	logger *slog.Logger
}

// New returns a Process Backend ready to manage capability runtime workers.
func New(logger *slog.Logger) *Backend {
	if logger == nil {
		logger = slog.Default()
	}
	return &Backend{
		pools:  make(map[string]*workerPool),
		logger: logger,
	}
}

// RuntimeName returns "process", matching RuntimeKindProcess.
func (r *Backend) BackendName() string {
	return capabilityrt.RuntimeKindProcess
}

// Start launches an capability runtime instance backed by a pool of long-lived
// worker processes. The pool is created lazily on the first request.
func (r *Backend) Start(ctx context.Context, spec capabilityrt.BackendSpec) (capabilityrt.BackendInstance, error) {
	if spec.Entrypoint == "" {
		return nil, fmt.Errorf("capability runtime %s: no entrypoint", spec.Type)
	}
	if _, err := os.Stat(spec.Entrypoint); err != nil {
		return nil, fmt.Errorf("capability runtime %s: entrypoint %s: %w", spec.Type, spec.Entrypoint, err)
	}

	protocol := spec.Protocol
	if protocol == "" {
		protocol = capabilityrt.ProtocolV1
	}

	switch protocol {
	case capabilityrt.ProtocolJSONV1:
		// Legacy stdin/stdout JSON transport: each execution spawns a fresh
		// process. This is the transport used by WASI capability runtimes and by
		// process capability runtimes that predate the gRPC protocol.
		return newLegacyInstance(spec, r.logger)
	case capabilityrt.ProtocolV1:
		// Canonical gRPC transport over Unix domain sockets with a pool of
		// long-lived worker processes.
		maxWorkers := spec.MaxWorkers
		if maxWorkers <= 0 {
			maxWorkers = defaultMaxWorkers
		}

		key := instanceKey(spec.Type, spec.Version)

		// Pools are shared by capability runtime identity so that every instance
		// of an assembly reuses the same warm workers instead of paying process
		// startup per instance. Sharing makes the pool's lifetime a joint
		// responsibility: it is refcounted by its holders and torn down only
		// when the last holder releases it. Replacing or closing the pool
		// outright here would terminate workers that other live instances are
		// still executing on.
		r.mu.Lock()
		if existing, ok := r.pools[key]; ok {
			existing.acquire()
			r.mu.Unlock()
			return existing, nil
		}
		pool := newWorkerPool(spec, protocol, maxWorkers,
			r.logger.With("capability_runtime", spec.Type, "version", spec.Version))
		// Deregister the pool when it tears down, so the next Start for this
		// capability runtime builds a fresh pool instead of receiving a closed
		// one whose workers are already gone. The identity check matters: a
		// teardown racing a later Start must not evict the newer pool.
		pool.onShutdown = func() {
			r.mu.Lock()
			if r.pools[key] == pool {
				delete(r.pools, key)
			}
			r.mu.Unlock()
		}
		r.pools[key] = pool
		r.mu.Unlock()

		return pool, nil
	default:
		return nil, fmt.Errorf("capability runtime %s: unsupported protocol %q (supported: %q, %q)",
			spec.Type, protocol, capabilityrt.ProtocolV1, capabilityrt.ProtocolJSONV1)
	}
}

// Close shuts down every pool this backend manages, regardless of how many
// holders each pool has. This is the backend-level teardown performed when the
// runtime itself is shutting down; per-instance teardown goes through the
// BackendInstance Close contract and only releases a single holder.
func (r *Backend) Close(ctx context.Context) error {
	r.mu.Lock()
	pools := make([]*workerPool, 0, len(r.pools))
	for _, p := range r.pools {
		pools = append(pools, p)
	}
	r.pools = make(map[string]*workerPool)
	r.mu.Unlock()

	var errs []error
	for _, p := range pools {
		if err := p.shutdown(ctx); err != nil {
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

// resolveEntrypoint returns the absolute entrypoint for a frozen capability runtime,
// handling both manifest-relative and absolute paths.
func resolveEntrypoint(rootDir, entrypoint string) string {
	if filepath.IsAbs(entrypoint) {
		return entrypoint
	}
	return filepath.Join(rootDir, filepath.FromSlash(entrypoint))
}
