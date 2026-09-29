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
type workerPool struct {
	type_      string
	version    string
	entrypoint string
	protocol   string
	maxWorkers int
	rootDir    string
	logger     *slog.Logger

	mu        sync.Mutex
	workers   []*worker
	available chan *worker
	closed    bool
}

func (p *workerPool) init() {
	if p.available == nil {
		p.available = make(chan *worker, p.maxWorkers)
	}
}

// Execute leases an available worker, sends the request, and returns the
// worker to the pool. If no worker is available and the pool is not at
// capacity, a new worker is started.
func (p *workerPool) Execute(ctx context.Context, req *capabilityrt.Request) (*capabilityrt.Response, error) {
	if req == nil {
		req = &capabilityrt.Request{}
	}

	p.init()

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

// Close gracefully shuts down all workers in the pool.
func (p *workerPool) Close(ctx context.Context) error {
	p.init()

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	workers := make([]*worker, len(p.workers))
	copy(workers, p.workers)
	p.workers = nil
	p.mu.Unlock()

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

// leaseWorker returns an available worker or starts a new one if under capacity.
func (p *workerPool) leaseWorker(ctx context.Context) (*worker, error) {
	p.init()

	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("pool is closed")
	}

	// Try to get an existing idle worker.
	select {
	case w := <-p.available:
		if w.healthy.Load() {
			return w, nil
		}
		w.close(context.Background())
		p.removeWorker(w)
	default:
		// No idle workers available.
	}

	// Start a new worker if under capacity.
	p.mu.Lock()
	underCapacity := len(p.workers) < p.maxWorkers
	closed = p.closed
	p.mu.Unlock()
	if underCapacity && !closed {
		w, err := p.startWorker(ctx)
		if err != nil {
			return nil, err
		}
		return w, nil
	}

	// Pool is at capacity; wait for a worker to become available.
	select {
	case w := <-p.available:
		if w.healthy.Load() {
			return w, nil
		}
		w.close(context.Background())
		p.removeWorker(w)
		return p.startWorker(ctx)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// returnWorker returns a worker to the available pool.
func (p *workerPool) returnWorker(w *worker) {
	if w == nil {
		return
	}
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		w.close(context.Background())
		return
	}

	select {
	case p.available <- w:
	default:
		w.close(context.Background())
	}
}

// removeWorker removes a worker from the pool.
func (p *workerPool) removeWorker(w *worker) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, pw := range p.workers {
		if pw == w {
			p.workers = append(p.workers[:i], p.workers[i+1:]...)
			return
		}
	}
}

// startWorker launches a new worker process and connects to it via gRPC.
func (p *workerPool) startWorker(ctx context.Context) (*worker, error) {
	socketDir, err := os.MkdirTemp("", "neuron-capability-runtime-*")
	if err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}

	socketPath := filepath.Join(socketDir, "capability-runtime.sock")

	cmd := exec.CommandContext(ctx, p.entrypoint)
	cmd.Env = append(os.Environ(),
		capabilityrt.EnvProtocol+"="+p.protocol,
		capabilityrt.EnvType+"="+p.type_,
		capabilityrt.EnvVersion+"="+p.version,
		"NEURON_CAPABILITY_RUNTIME_SOCKET="+socketPath,
	)
	if p.rootDir != "" {
		cmd.Dir = p.rootDir
	}

	// The capability runtime process signals readiness by writing this file when its
	// gRPC server is accepting connections.
	readyFile := filepath.Join(socketDir, "ready")
	cmd.Env = append(cmd.Env, "NEURON_CAPABILITY_RUNTIME_READY="+readyFile)

	if err := cmd.Start(); err != nil {
		os.RemoveAll(socketDir)
		return nil, fmt.Errorf("start capability runtime process: %w", err)
	}

	w := &worker{
		process:    cmd.Process,
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

	p.mu.Lock()
	p.workers = append(p.workers, w)
	p.mu.Unlock()

	p.logger.Info("started capability runtime worker",
		"pid", cmd.Process.Pid,
		"socket", socketPath,
	)

	return w, nil
}

// worker represents a single capability runtime process.
type worker struct {
	process    *os.Process
	socketPath string
	socketDir  string
	readyFile  string
	type_      string
	version    string
	conn       *grpc.ClientConn
	client     v1.CapabilityRuntimeServiceClient
	healthy    atomic.Bool
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

// close gracefully shuts down the worker process.
func (w *worker) close(ctx context.Context) error {
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
		done := make(chan struct{})
		go func() {
			state, _ := w.process.Wait()
			if state != nil {
				w.logger.Debug("capability runtime worker exited", "exit_code", state.ExitCode())
			}
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(defaultShutdownTimeout):
			_ = w.process.Kill()
			<-done
		}
	}

	os.RemoveAll(w.socketDir)

	w.logger.Info("stopped capability runtime worker")
	return nil
}
