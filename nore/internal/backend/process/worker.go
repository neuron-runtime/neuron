package process

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	v1 "github.com/neuron-runtime/neuron/shared/protocol/capabilityruntime/v1"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// workerPool manages a pool of long-lived gRPC worker processes for one
// capability runtime type. Requests are leased to available workers.
//
// The pool owns the lifetime of its worker processes, which is deliberately
// independent of any individual request: a worker is started once, reused
// across requests, and terminated only when the pool closes. Worker processes
// are therefore never bound to a request context, because a caller that
// disconnects must not take down a worker that other requests are queued on.
type workerPool struct {
	type_      string
	version    string
	entrypoint string
	protocol   string
	maxWorkers int
	rootDir    string
	logger     *slog.Logger

	// available is created by newWorkerPool and is never reassigned, so it can
	// be read without holding mu.
	available chan *worker

	// mu guards the worker set, the reserved slot count, and the holder
	// refcount. It is never held across a worker start, a network call, or any
	// other blocking operation.
	mu       sync.Mutex
	workers  []*worker
	reserved int
	closed   bool
	holders  int

	// onShutdown deregisters this pool from its owner when the pool is torn
	// down. Without it a closed pool would stay reachable through the owner's
	// index, and the next Start would hand back a pool whose workers are gone.
	//
	// It is invoked after mu is released, so it may take the owner's lock.
	onShutdown func()
}

// newWorkerPool constructs a pool with its availability channel sized to the
// worker capacity. The channel is fixed for the pool's lifetime: sizing it
// lazily at first use would require synchronisation on every request.
//
// A pool starts with one holder, the Start call that created it. Each
// additional Start returning the same pool adds a holder, and each release
// removes one.
func newWorkerPool(spec capabilityrt.BackendSpec, protocol string, maxWorkers int, logger *slog.Logger) *workerPool {
	return &workerPool{
		type_:      spec.Type,
		version:    spec.Version,
		entrypoint: spec.Entrypoint,
		protocol:   protocol,
		maxWorkers: maxWorkers,
		rootDir:    spec.RootDir,
		available:  make(chan *worker, maxWorkers),
		holders:    1,
		logger:     logger,
	}
}

// acquire records an additional holder of this pool. It is called when Start
// hands an already-running pool to another instance.
func (p *workerPool) acquire() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.holders++
}

// release drops one holder and terminates the pool's workers once the last
// holder is gone. This is what lets several instances share warm workers without
// any one instance's shutdown taking down the others.
//
// It is safe to call more times than acquire: the pool is torn down at most once.
func (p *workerPool) release(ctx context.Context) error {
	p.mu.Lock()
	if p.holders > 0 {
		p.holders--
	}
	last := p.holders == 0
	p.mu.Unlock()

	if !last {
		return nil
	}
	return p.shutdown(ctx)
}

// Close implements the BackendInstance contract, which is per-holder: it
// releases this caller's claim on the shared pool. The underlying workers are
// terminated only when no other instance still holds the pool. Use Backend.Close
// to force teardown of every pool regardless of holders.
func (p *workerPool) Close(ctx context.Context) error {
	return p.release(ctx)
}

// shutdown terminates every worker in the pool. It is idempotent.
func (p *workerPool) shutdown(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.holders = 0
	workers := make([]*worker, len(p.workers))
	copy(workers, p.workers)
	p.workers = nil
	p.reserved = 0
	onShutdown := p.onShutdown
	p.mu.Unlock()

	// Deregister before reaping, and outside the lock: the owner takes its own
	// lock here, and Start takes the owner's lock before a pool's own.
	if onShutdown != nil {
		onShutdown()
	}

	var errs []error
	for _, w := range workers {
		if err := w.close(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("close capability runtime %s workers: %v", p.type_, errs)
	}
	return nil
}

// Execute leases an available worker, sends the request, and returns the
// worker to the pool. If no worker is available and the pool is not at
// capacity, a new worker is started.
func (p *workerPool) Execute(ctx context.Context, req *capabilityrt.Request) (*capabilityrt.Response, error) {
	if req == nil {
		req = &capabilityrt.Request{}
	}

	// Bound the execution so a wedged capability runtime cannot hold a worker
	// indefinitely. A caller-supplied deadline always wins; this only applies
	// when the caller supplied none.
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultExecutionTimeout())
		defer cancel()
	}

	worker, err := p.leaseWorker(ctx)
	if err != nil {
		return nil, fmt.Errorf("capability runtime %s: %w", p.type_, err)
	}

	resp, err := worker.execute(ctx, req)
	p.returnWorker(worker)
	return resp, err
}

// Health reports whether the pool has at least one healthy worker available.
func (p *workerPool) Health(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return fmt.Errorf("capability runtime %s: pool is closed", p.type_)
	}

	if len(p.workers) == 0 {
		// No workers yet; this is acceptable if no requests have arrived.
		return nil
	}

	for _, w := range p.workers {
		if w.healthy.Load() {
			return nil
		}
	}

	return fmt.Errorf("capability runtime %s: no healthy workers", p.type_)
}

// workerCount reports the number of live workers. It exists so tests can assert
// on pool behaviour without racing the pool's own lock.
func (p *workerPool) workerCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.workers)
}

// reserveSlot reports whether a new worker may be started, and if so claims one
// of the pool's capacity slots on the caller's behalf.
//
// The claim is made under the lock and released later by releaseSlot, so
// concurrent leasers cannot collectively exceed maxWorkers. Without the
// reservation, two goroutines could both observe len(workers) < maxWorkers and
// both start a process, silently oversubscribing the pool.
func (p *workerPool) reserveSlot() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return false
	}
	if len(p.workers)+p.reserved >= p.maxWorkers {
		return false
	}
	p.reserved++
	return true
}

// releaseSlot returns a slot claimed by reserveSlot.
func (p *workerPool) releaseSlot() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reserved > 0 {
		p.reserved--
	}
}

// discardWorker terminates a worker and removes it from the pool, returning its
// reserved slot so the pool can start a replacement.
func (p *workerPool) discardWorker(w *worker) {
	if w == nil {
		return
	}
	_ = w.close(context.Background())

	p.mu.Lock()
	defer p.mu.Unlock()
	for i, pw := range p.workers {
		if pw == w {
			p.workers = append(p.workers[:i], p.workers[i+1:]...)
			break
		}
	}
	if p.reserved > 0 {
		p.reserved--
	}
}

// leaseWorker returns a healthy worker, starting a new one when the pool is
// under capacity, or waiting for one to be returned when it is at capacity.
//
// Every wait is bounded by ctx, which Execute has already given a deadline, so
// a capability runtime that never answers cannot block its peers indefinitely.
func (p *workerPool) leaseWorker(ctx context.Context) (*worker, error) {
	// Prefer an idle worker, but drain the channel fully so a crashed worker
	// at the head does not mask healthy workers behind it.
	for {
		select {
		case w := <-p.available:
			if w.healthy.Load() {
				return w, nil
			}
			p.discardWorker(w)
		default:
			goto drainDone
		}
	}

drainDone:
	if p.reserveSlot() {
		w, err := p.startWorker(ctx)
		if err != nil {
			p.releaseSlot()
			return nil, err
		}
		return w, nil
	}

	select {
	case w := <-p.available:
		if w.healthy.Load() {
			return w, nil
		}
		p.discardWorker(w)
		return p.leaseWorker(ctx)
	case <-ctx.Done():
		return nil, fmt.Errorf("pool at capacity (%d workers) and no worker became available: %w", p.maxWorkers, ctx.Err())
	}
}

// returnWorker returns a worker to the available pool. A worker that has become
// unhealthy, or a pool that has closed while the request was in flight, is
// terminated instead of being handed to the next request.
func (p *workerPool) returnWorker(w *worker) {
	if w == nil {
		return
	}

	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()

	if closed || !w.healthy.Load() {
		p.discardWorker(w)
		return
	}

	select {
	case p.available <- w:
	default:
		// The channel is sized to the pool capacity, so reaching the default
		// branch means the pool shrank under us. The worker is surplus.
		_ = w.close(context.Background())
		p.mu.Lock()
		for i, pw := range p.workers {
			if pw == w {
				p.workers = append(p.workers[:i], p.workers[i+1:]...)
				break
			}
		}
		if p.reserved > 0 {
			p.reserved--
		}
		p.mu.Unlock()
	}
}

// startWorker launches a new worker process and connects to it via gRPC.
//
// The caller must already hold a capacity slot reserved via reserveSlot.
//
// The process is started from a pool-lifetime context rather than the request
// context. The worker outlives the request that caused it to start, so binding
// it to that request would let an unrelated caller's cancellation kill a worker
// that other requests depend on.
func (p *workerPool) startWorker(ctx context.Context) (*worker, error) {
	socketDir, err := os.MkdirTemp("", "neuron-capability-runtime-*")
	if err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}

	socketPath := filepath.Join(socketDir, "capability-runtime.sock")

	// Strip cancellation from the request context: the process lifetime is
	// owned by the pool, while readiness and the handshake below are still
	// bounded by the caller's deadline.
	//
	// On success ownership of stopProcess transfers to the worker, which
	// invokes it from close. It must not be deferred here, or the worker would
	// be killed the moment startWorker returned. Every failure path below
	// therefore releases it explicitly before returning.
	processCtx, stopProcess := context.WithCancel(context.WithoutCancel(ctx))

	cmd := exec.CommandContext(processCtx, p.entrypoint)
	cmd.Env = append(p.workerEnv(),
		capabilityrt.EnvProtocol+"="+p.protocol,
		capabilityrt.EnvType+"="+p.type_,
		capabilityrt.EnvVersion+"="+p.version,
		capabilityrt.EnvSocket+"="+socketPath,
	)
	if p.rootDir != "" {
		cmd.Dir = p.rootDir
	}

	// The capability runtime process signals readiness by writing this file when its
	// gRPC server is accepting connections.
	readyFile := filepath.Join(socketDir, "ready")
	cmd.Env = append(cmd.Env, capabilityrt.EnvReady+"="+readyFile)

	if err := cmd.Start(); err != nil {
		stopProcess()
		os.RemoveAll(socketDir)
		return nil, fmt.Errorf("start capability runtime process: %w", err)
	}

	w := &worker{
		process:    cmd.Process,
		cancelProc: stopProcess,
		socketPath: socketPath,
		socketDir:  socketDir,
		readyFile:  readyFile,
		type_:      p.type_,
		version:    p.version,
		logger:     p.logger.With("pid", cmd.Process.Pid),
	}

	startCtx, cancel := context.WithTimeout(ctx, defaultStartTimeout)
	defer cancel()

	if err := w.waitForReady(startCtx); err != nil {
		w.close(context.Background())
		return nil, fmt.Errorf("wait for capability runtime ready: %w", err)
	}

	if err := w.connect(startCtx); err != nil {
		w.close(context.Background())
		return nil, fmt.Errorf("connect to capability runtime: %w", err)
	}

	if err := w.initialize(startCtx, p.protocol); err != nil {
		w.close(context.Background())
		return nil, fmt.Errorf("initialize capability runtime: %w", err)
	}

	// The reserved slot converts into a live worker here.
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		w.close(context.Background())
		return nil, fmt.Errorf("capability runtime %s: pool closed while starting worker", p.type_)
	}
	p.workers = append(p.workers, w)
	if p.reserved > 0 {
		p.reserved--
	}
	p.mu.Unlock()

	p.logger.Info("started capability runtime worker",
		"pid", cmd.Process.Pid,
		"socket", socketPath,
	)

	return w, nil
}

// workerEnv builds the environment for a capability runtime child process.
//
// External capability runtimes are untrusted code, so the child receives an
// explicit allowlist rather than a copy of the daemon's environment. Inheriting
// os.Environ() wholesale would hand every installed artifact whatever the
// daemon happens to hold: data-directory paths, socket locations, and any
// credentials present in CI or the operator's shell.
//
// The allowlist is intentionally small. A capability runtime that needs
// additional environment must declare it in its runtime.json manifest, and
// that declaration is an explicit, reviewable act rather than ambient
// inheritance.
func (p *workerPool) workerEnv() []string {
	allowlist := []string{
		"PATH",
		"HOME",
		"LANG",
		"LC_ALL",
		"TZ",
		"TMPDIR",
		"SSL_CERT_FILE",
		"SSL_CERT_DIR",
	}

	env := make([]string, 0, len(allowlist)+8)
	for _, key := range allowlist {
		if val, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+val)
		}
	}
	return env
}

// worker represents a single capability runtime process.
type worker struct {
	process    *os.Process
	cancelProc context.CancelFunc
	socketPath string
	socketDir  string
	readyFile  string
	type_      string
	version    string
	conn       *grpc.ClientConn
	client     v1.CapabilityRuntimeServiceClient
	healthy    atomic.Bool
	closeOnce  sync.Once
	logger     *slog.Logger
}

// waitForReady blocks until the capability runtime signals readiness or the context
// times out. The capability runtime writes a file to readyFile when its gRPC server
// is accepting connections.
func (w *worker) waitForReady(ctx context.Context) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := os.Stat(w.readyFile); err == nil {
				return nil
			}
		}
	}
}

// connect establishes a gRPC connection to the capability runtime process.
func (w *worker) connect(ctx context.Context) error {
	conn, err := grpc.NewClient(
		"unix://"+w.socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("dial capability runtime: %w", err)
	}
	w.conn = conn
	w.client = v1.NewCapabilityRuntimeServiceClient(conn)
	return nil
}

// initialize performs the gRPC Initialize handshake.
func (w *worker) initialize(ctx context.Context, protocol string) error {
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := w.client.Initialize(initCtx, &v1.InitializeRequest{
		ProtocolVersion: protocol,
		Metadata: map[string]string{
			"capability_runtime_type":    w.type_,
			"capability_runtime_version": w.version,
		},
	})
	if err != nil {
		return fmt.Errorf("initialize handshake: %w", err)
	}

	if resp.ProtocolVersion == "" {
		return fmt.Errorf("capability runtime %s: returned empty protocol version", w.type_)
	}

	w.logger.Info("initialized capability runtime",
		"protocol", resp.ProtocolVersion,
		"features", resp.Features,
	)

	w.healthy.Store(true)
	return nil
}

// execute sends an execution request to the capability runtime process via gRPC.
func (w *worker) execute(ctx context.Context, req *capabilityrt.Request) (*capabilityrt.Response, error) {
	if !w.healthy.Load() {
		return nil, fmt.Errorf("worker is unhealthy")
	}

	execReq := &v1.ExecuteRequest{
		Params: make(map[string]*v1.Value, len(req.Params)),
	}
	for k, v := range req.Params {
		execReq.Params[k] = makeProtoValue(v)
	}

	resp, err := w.client.Execute(ctx, execReq)
	if err != nil {
		return nil, fmt.Errorf("execute via gRPC: %w", err)
	}

	out := &capabilityrt.Response{
		Result: make(map[string]any, len(resp.Result)),
		Error:  resp.Error,
	}
	for k, v := range resp.Result {
		out.Result[k] = makeGoValue(v)
	}

	return out, nil
}

// close gracefully shuts down the worker process. It is idempotent: a worker
// may be closed from the pool's shutdown path and from the request path that
// discovered it unhealthy, and both must not race to reap the same process.
func (w *worker) close(ctx context.Context) error {
	var err error
	w.closeOnce.Do(func() {
		err = w.shutdown(ctx)
	})
	return err
}

func (w *worker) shutdown(ctx context.Context) error {
	w.healthy.Store(false)

	if w.client != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, defaultShutdownTimeout)
		defer cancel()
		_, _ = w.client.Shutdown(shutdownCtx, &v1.ShutdownRequest{
			TimeoutMs: int64(defaultShutdownTimeout.Milliseconds()),
		})
	}

	if w.conn != nil {
		_ = w.conn.Close()
	}

	if w.process != nil {
		// Release the pool-owned process context first so the child is signalled
		// even if Wait is still blocked.
		if w.cancelProc != nil {
			w.cancelProc()
		}

		done := make(chan struct{})
		go func() {
			// Signal completion on every path. Shutdown blocks on <-done after
			// killing a worker that ignored the graceful stop, so a wait error
			// that skipped this close would hang shutdown forever.
			defer close(done)
			state, waitErr := w.process.Wait()
			if waitErr != nil {
				w.logger.Debug("capability runtime worker wait failed", "error", waitErr)
				return
			}
			if state != nil {
				w.logger.Debug("capability runtime worker exited", "exit_code", state.ExitCode())
			}
		}()

		select {
		case <-done:
		case <-time.After(defaultShutdownTimeout):
			_ = w.process.Kill()
			<-done
		}
	}

	_ = os.RemoveAll(w.socketDir)

	w.logger.Info("stopped capability runtime worker")
	return nil
}
