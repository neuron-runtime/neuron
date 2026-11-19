package process

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/neuron-runtime/neuron/shared/protocol/capabilityruntime/v1"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type suspendServer struct {
	v1.UnimplementedCapabilityRuntimeServiceServer
}

func (s *suspendServer) Execute(context.Context, *v1.ExecuteRequest) (*v1.ExecuteResponse, error) {
	return &v1.ExecuteResponse{
		Suspend: &v1.SuspendOutcome{
			WaitType:       "signal",
			ExpectedSignal: "approval",
			CorrelationKey: "corr-1",
		},
	}, nil
}

// TestWorkerExecuteRejectsSuspendOutcome proves a suspend response cannot become
// a successful empty result at the worker/client boundary. Until T-026 implements
// suspend/resume, the worker must fail closed with an explicit error.
func TestWorkerExecuteRejectsSuspendOutcome(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "s.sock")

	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	srv := grpc.NewServer()
	v1.RegisterCapabilityRuntimeServiceServer(srv, &suspendServer{})
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	t.Cleanup(func() {
		srv.GracefulStop()
		_ = ln.Close()
		select {
		case err := <-serveErr:
			if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				t.Errorf("grpc server: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("grpc server did not stop")
		}
	})

	conn, err := grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	w := &worker{
		type_:   "example:suspend",
		version: "1.0.0",
		conn:    conn,
		client:  v1.NewCapabilityRuntimeServiceClient(conn),
		logger:  slog.New(slog.DiscardHandler),
	}
	w.healthy.Store(true)

	resp, err := w.execute(context.Background(), &capabilityrt.Request{Params: map[string]any{"x": "y"}})
	if err == nil {
		t.Fatalf("execute() returned success with response %#v, want unsupported_suspend_outcome error", resp)
	}
	if !strings.Contains(err.Error(), "unsupported_suspend_outcome") {
		t.Fatalf("execute() error = %q, want unsupported_suspend_outcome", err)
	}
	if resp != nil {
		t.Fatalf("execute() response = %#v, want nil on suspend rejection", resp)
	}
}
