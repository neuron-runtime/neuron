package process

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/neuron-runtime/neuron/shared/protocol/capabilityruntime/v1"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// conformanceServer is a real gRPC capability runtime used to exercise the
// worker's actual Initialize/Execute paths. It records the requests it receives
// so tests can assert resume delivery without inspecting private helpers.
type conformanceServer struct {
	v1.UnimplementedCapabilityRuntimeServiceServer

	features []string
	response *v1.ExecuteResponse

	mu       sync.Mutex
	requests []*v1.ExecuteRequest
}

func (s *conformanceServer) Initialize(context.Context, *v1.InitializeRequest) (*v1.InitializeResponse, error) {
	return &v1.InitializeResponse{ProtocolVersion: "v1", Features: s.features}, nil
}

func (s *conformanceServer) Execute(_ context.Context, req *v1.ExecuteRequest) (*v1.ExecuteResponse, error) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	return s.response, nil
}

func (s *conformanceServer) received() []*v1.ExecuteRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*v1.ExecuteRequest(nil), s.requests...)
}

// newConformanceWorker starts a gRPC capability runtime with the given
// negotiated features and Execute response, then performs the real Initialize
// handshake against it.
func newConformanceWorker(t *testing.T, features []string, response *v1.ExecuteResponse) (*worker, *conformanceServer) {
	t.Helper()
	dir := t.TempDir()
	socket := filepath.Join(dir, "s.sock")

	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	srv := grpc.NewServer()
	server := &conformanceServer{features: features, response: response}
	v1.RegisterCapabilityRuntimeServiceServer(srv, server)
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
	if err := w.initialize(context.Background(), "v1"); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return w, server
}

func signalSuspend() *v1.ExecuteResponse {
	return &v1.ExecuteResponse{Suspend: &v1.SuspendOutcome{
		CorrelationKey: "corr-1",
		WaitType:       "signal",
		ExpectedSignal: "approval",
		PayloadSchema:  "approval/v1",
	}}
}

// TestConformanceSignalSuspendNegotiated proves a negotiated runtime's signal
// suspend reaches the response envelope with its metadata intact and no result.
func TestConformanceSignalSuspendNegotiated(t *testing.T) {
	w, _ := newConformanceWorker(t, []string{v1.SuspendFeature}, signalSuspend())

	resp, err := w.execute(context.Background(), &capabilityrt.Request{Params: map[string]any{"x": "y"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Suspend == nil {
		t.Fatal("negotiated signal suspend was dropped")
	}
	want := &capabilityrt.Suspend{
		CorrelationKey: "corr-1",
		WaitType:       "signal",
		ExpectedSignal: "approval",
		PayloadSchema:  "approval/v1",
	}
	if *resp.Suspend != *want {
		t.Errorf("suspend = %#v, want %#v", resp.Suspend, want)
	}
	if len(resp.Result) != 0 {
		t.Errorf("suspend response result = %#v, want empty", resp.Result)
	}
	if resp.Suspend.DeadlineUnixMs != 0 {
		t.Errorf("signal deadline = %d, want 0", resp.Suspend.DeadlineUnixMs)
	}
}

// TestConformanceTimerSuspendDeadlineConversion proves deadline_unix_ms is
// converted to WaitUntil on the typed suspend, not merely carried as a wire
// integer. The wire value is asserted through the real Execute path; the typed
// conversion is asserted on the same mapping result.
func TestConformanceTimerSuspendDeadlineConversion(t *testing.T) {
	const deadlineMs int64 = 1_760_000_000_123
	w, _ := newConformanceWorker(t, []string{v1.SuspendFeature}, &v1.ExecuteResponse{
		Suspend: &v1.SuspendOutcome{
			CorrelationKey: "timer-1",
			WaitType:       "timer",
			DeadlineUnixMs: deadlineMs,
		},
	})

	resp, err := w.execute(context.Background(), &capabilityrt.Request{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Suspend == nil || resp.Suspend.WaitType != "timer" || resp.Suspend.DeadlineUnixMs != deadlineMs {
		t.Fatalf("timer suspend = %#v", resp.Suspend)
	}

	typed, err := mapSuspendOutcome(&v1.SuspendOutcome{WaitType: "timer", DeadlineUnixMs: deadlineMs})
	if err != nil {
		t.Fatalf("map timer suspend: %v", err)
	}
	want := time.UnixMilli(deadlineMs).UTC()
	if typed.WaitUntil == nil || !typed.WaitUntil.Equal(want) {
		t.Errorf("WaitUntil = %v, want %v", typed.WaitUntil, want)
	}
	if typed.WaitType != "timer" {
		t.Errorf("typed wait type = %q, want timer", typed.WaitType)
	}
}

// TestConformanceSuspendRequiresNegotiatedFeature proves an old runtime (no
// features) and a runtime advertising an unrelated feature both get an explicit
// error instead of an empty success.
func TestConformanceSuspendRequiresNegotiatedFeature(t *testing.T) {
	cases := []struct {
		name     string
		features []string
	}{
		{"old runtime with no features", nil},
		{"unnegotiated feature", []string{"streaming"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, _ := newConformanceWorker(t, tc.features, signalSuspend())
			resp, err := w.execute(context.Background(), &capabilityrt.Request{})
			if err == nil || !strings.Contains(err.Error(), "unsupported_suspend_outcome") {
				t.Fatalf("execute() = (%#v, %v), want unsupported_suspend_outcome", resp, err)
			}
			if resp != nil {
				t.Fatalf("response = %#v, want nil on unnegotiated suspend", resp)
			}
		})
	}
}

// TestConformanceMalformedSuspendMetadata exercises invalid, untrusted
// executor metadata through the real Execute path. Every case must fail
// explicitly; none may become an empty successful result.
func TestConformanceMalformedSuspendMetadata(t *testing.T) {
	long := strings.Repeat("x", 257)
	cases := []struct {
		name string
		resp *v1.ExecuteResponse
	}{
		{"reserved deadline wait type", &v1.ExecuteResponse{Suspend: &v1.SuspendOutcome{WaitType: "deadline", DeadlineUnixMs: 1}}},
		{"unknown wait type", &v1.ExecuteResponse{Suspend: &v1.SuspendOutcome{WaitType: "anything"}}},
		{"empty wait type", &v1.ExecuteResponse{Suspend: &v1.SuspendOutcome{}}},
		{"signal without expected signal", &v1.ExecuteResponse{Suspend: &v1.SuspendOutcome{WaitType: "signal"}}},
		{"timer without deadline", &v1.ExecuteResponse{Suspend: &v1.SuspendOutcome{WaitType: "timer"}}},
		{"negative deadline", &v1.ExecuteResponse{Suspend: &v1.SuspendOutcome{WaitType: "signal", ExpectedSignal: "s", DeadlineUnixMs: -1}}},
		{"oversized correlation key", &v1.ExecuteResponse{Suspend: &v1.SuspendOutcome{WaitType: "signal", ExpectedSignal: "s", CorrelationKey: long}}},
		{"oversized expected signal", &v1.ExecuteResponse{Suspend: &v1.SuspendOutcome{WaitType: "signal", ExpectedSignal: long}}},
		{"oversized payload schema", &v1.ExecuteResponse{Suspend: &v1.SuspendOutcome{WaitType: "signal", ExpectedSignal: "s", PayloadSchema: long}}},
		{"suspend with result", &v1.ExecuteResponse{
			Suspend: &v1.SuspendOutcome{WaitType: "signal", ExpectedSignal: "s"},
			Result:  map[string]*v1.Value{"x": {Kind: &v1.Value_StringValue{StringValue: "y"}}},
		}},
		{"suspend with error", &v1.ExecuteResponse{
			Suspend: &v1.SuspendOutcome{WaitType: "signal", ExpectedSignal: "s"},
			Error:   "also failed",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, _ := newConformanceWorker(t, []string{v1.SuspendFeature}, tc.resp)
			resp, err := w.execute(context.Background(), &capabilityrt.Request{})
			if err == nil {
				t.Fatalf("execute() returned success with %#v; want explicit rejection", resp)
			}
			if resp != nil {
				t.Fatalf("response = %#v, want nil on malformed suspend", resp)
			}
		})
	}
}

// TestConformanceResumeSignalAndDuplicateDelivery proves resume inputs cross
// the process transport with payload and delivery ID intact, and that repeated
// delivery of the same ID is presented to the runtime twice: idempotency is the
// capability's responsibility, not something the transport silently performs.
func TestConformanceResumeSignalAndDuplicateDelivery(t *testing.T) {
	w, server := newConformanceWorker(t, []string{v1.SuspendFeature}, &v1.ExecuteResponse{
		Result: map[string]*v1.Value{"ok": {Kind: &v1.Value_BoolValue{BoolValue: true}}},
	})
	req := &capabilityrt.Request{
		Params: map[string]any{"x": "y"},
		Resume: &capabilityrt.ResumeInput{
			Cause:      "signal",
			SignalName: "approval",
			DeliveryID: "delivery-1",
			Payload:    map[string]any{"by": "ada"},
		},
	}

	for i := 0; i < 2; i++ {
		if _, err := w.execute(context.Background(), req); err != nil {
			t.Fatalf("execute #%d: %v", i+1, err)
		}
	}

	received := server.received()
	if len(received) != 2 {
		t.Fatalf("runtime received %d requests, want 2", len(received))
	}
	for i, got := range received {
		if got.Resume == nil {
			t.Fatalf("request #%d has no resume input", i+1)
		}
		if got.Resume.Cause != "signal" || got.Resume.SignalName != "approval" || got.Resume.DeliveryId != "delivery-1" {
			t.Errorf("request #%d resume = %#v", i+1, got.Resume)
		}
		if got.Resume.Payload["by"].GetStringValue() != "ada" {
			t.Errorf("request #%d resume payload = %#v", i+1, got.Resume.Payload)
		}
	}
}

// TestConformanceResumeTimer proves a timer resume carries its cause without a
// signal name or payload while preserving the stable delivery ID.
func TestConformanceResumeTimer(t *testing.T) {
	w, server := newConformanceWorker(t, []string{v1.SuspendFeature}, &v1.ExecuteResponse{})
	_, err := w.execute(context.Background(), &capabilityrt.Request{
		Resume: &capabilityrt.ResumeInput{Cause: "timer", DeliveryID: "delivery-timer"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	received := server.received()
	if len(received) != 1 || received[0].Resume == nil {
		t.Fatalf("resume not delivered: %#v", received)
	}
	got := received[0].Resume
	if got.Cause != "timer" || got.DeliveryId != "delivery-timer" || got.SignalName != "" || len(got.Payload) != 0 {
		t.Errorf("timer resume = %#v", got)
	}
}
