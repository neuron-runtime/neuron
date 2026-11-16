package capabilityruntime

import (
	"context"
	"os"
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

// startConformanceServer hosts handler through the real gRPC transport and
// returns a client wired to it. Tests exercise the same boundary a runtime
// author uses: Serve plus the generated capability-runtime contract.
func startConformanceServer(t *testing.T, handler Handler) v1.CapabilityRuntimeServiceClient {
	t.Helper()

	dir := t.TempDir()
	socketPath := filepath.Join(dir, "capability.sock")
	readyPath := filepath.Join(dir, "ready")
	t.Setenv(capabilityrt.EnvSocket, socketPath)
	t.Setenv(capabilityrt.EnvReady, readyPath)

	done := make(chan error, 1)
	go func() { done <- Serve(handler) }()

	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("capability runtime did not signal readiness")
		}
		time.Sleep(10 * time.Millisecond)
	}

	conn, err := grpc.NewClient(
		"unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial capability runtime: %v", err)
	}
	client := v1.NewCapabilityRuntimeServiceClient(conn)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = client.Shutdown(ctx, &v1.ShutdownRequest{})
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Errorf("capability runtime did not shut down")
		}
		_ = conn.Close()
	})

	return client
}

func initialize(t *testing.T, client v1.CapabilityRuntimeServiceClient) *v1.InitializeResponse {
	t.Helper()
	resp, err := client.Initialize(context.Background(), &v1.InitializeRequest{
		ProtocolVersion: capabilityrt.ProtocolV1,
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return resp
}

func execute(t *testing.T, client v1.CapabilityRuntimeServiceClient, req *v1.ExecuteRequest) *v1.ExecuteResponse {
	t.Helper()
	resp, err := client.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return resp
}

func stringValue(s string) *v1.Value {
	return &v1.Value{Kind: &v1.Value_StringValue{StringValue: s}}
}

// TestConformanceOldClientNoFeature proves a runtime that does not advertise
// suspend behaves exactly as before: it returns a normal result, ignores the
// unknown suspend field, and receives no resume input on a first invocation.
func TestConformanceOldClientNoFeature(t *testing.T) {
	client := startConformanceServer(t, Handler{
		Initialize: func(context.Context, string, map[string]string) (*InitializeResult, error) {
			return &InitializeResult{}, nil
		},
		ExecuteWithRequest: func(_ context.Context, req ExecutionRequest) (*ExecutionResult, error) {
			return &ExecutionResult{Result: map[string]any{
				"echo":   req.Params["value"],
				"resume": req.Resume != nil,
			}}, nil
		},
	})

	init := initialize(t, client)
	if v1.HasFeature(init.Features, v1.SuspendFeature) {
		t.Fatalf("features = %v, want no %q", init.Features, v1.SuspendFeature)
	}

	resp := execute(t, client, &v1.ExecuteRequest{
		Params: map[string]*v1.Value{"value": stringValue("old-client")},
	})

	if resp.Suspend != nil {
		t.Fatalf("suspend = %v, want nil for an old client", resp.Suspend)
	}
	if resp.Error != "" {
		t.Fatalf("error = %q, want empty", resp.Error)
	}
	if got := resp.Result["echo"].GetStringValue(); got != "old-client" {
		t.Errorf("echo = %q, want %q", got, "old-client")
	}
	if resp.Result["resume"].GetBoolValue() {
		t.Errorf("resume delivered to a first invocation")
	}
}

// TestConformanceNegotiatedSuspend proves a runtime that advertises suspend can
// return a suspend outcome, and that the outcome round-trips over the wire.
func TestConformanceNegotiatedSuspend(t *testing.T) {
	client := startConformanceServer(t, Handler{
		Initialize: func(context.Context, string, map[string]string) (*InitializeResult, error) {
			return &InitializeResult{Features: []string{v1.SuspendFeature}}, nil
		},
		ExecuteWithRequest: func(context.Context, ExecutionRequest) (*ExecutionResult, error) {
			return &ExecutionResult{Suspend: &SuspendOutcome{
				CorrelationKey: "corr-1",
				WaitType:       WaitTypeSignal,
				ExpectedSignal: "approval",
				DeadlineUnixMs: 4242,
				PayloadSchema:  "approval/v1",
			}}, nil
		},
	})

	init := initialize(t, client)
	if !v1.HasFeature(init.Features, v1.SuspendFeature) {
		t.Fatalf("features = %v, want %q", init.Features, v1.SuspendFeature)
	}

	resp := execute(t, client, &v1.ExecuteRequest{})
	if resp.Error != "" {
		t.Fatalf("error = %q, want empty", resp.Error)
	}
	if resp.Suspend == nil {
		t.Fatal("suspend = nil, want a suspend outcome")
	}
	if got := resp.Suspend.CorrelationKey; got != "corr-1" {
		t.Errorf("correlation_key = %q, want %q", got, "corr-1")
	}
	if got := resp.Suspend.WaitType; got != WaitTypeSignal {
		t.Errorf("wait_type = %q, want %q", got, WaitTypeSignal)
	}
	if got := resp.Suspend.ExpectedSignal; got != "approval" {
		t.Errorf("expected_signal = %q, want %q", got, "approval")
	}
	if got := resp.Suspend.DeadlineUnixMs; got != 4242 {
		t.Errorf("deadline_unix_ms = %d, want %d", got, 4242)
	}
	if got := resp.Suspend.PayloadSchema; got != "approval/v1" {
		t.Errorf("payload_schema = %q, want %q", got, "approval/v1")
	}
	if len(resp.Result) != 0 {
		t.Errorf("result = %v, want empty for a suspend outcome", resp.Result)
	}
}

// TestConformanceUnnegotiatedSuspendRejected proves a runtime cannot emit a
// suspend outcome it did not negotiate: the SDK fails explicitly instead of
// returning an empty successful result.
func TestConformanceUnnegotiatedSuspendRejected(t *testing.T) {
	client := startConformanceServer(t, Handler{
		Initialize: func(context.Context, string, map[string]string) (*InitializeResult, error) {
			return &InitializeResult{}, nil
		},
		ExecuteWithRequest: func(context.Context, ExecutionRequest) (*ExecutionResult, error) {
			return &ExecutionResult{Suspend: &SuspendOutcome{
				WaitType:       WaitTypeSignal,
				ExpectedSignal: "approval",
			}}, nil
		},
	})

	init := initialize(t, client)
	if v1.HasFeature(init.Features, v1.SuspendFeature) {
		t.Fatalf("features = %v, want no %q", init.Features, v1.SuspendFeature)
	}

	resp := execute(t, client, &v1.ExecuteRequest{})
	if resp.Suspend != nil {
		t.Fatalf("suspend = %v, want nil for an unnegotiated outcome", resp.Suspend)
	}
	if !strings.Contains(resp.Error, "unsupported_suspend_outcome") {
		t.Errorf("error = %q, want an unsupported_suspend_outcome failure", resp.Error)
	}
	if len(resp.Result) != 0 {
		t.Errorf("result = %v, want empty on rejection", resp.Result)
	}
}

// TestConformanceSignalResume proves a suspend can be resumed by a fresh
// Execute carrying a signal resume input, with a stable logical operation ID
// and a new attempt ID.
func TestConformanceSignalResume(t *testing.T) {
	var (
		mu    sync.Mutex
		calls int
	)
	client := startConformanceServer(t, Handler{
		Initialize: func(context.Context, string, map[string]string) (*InitializeResult, error) {
			return &InitializeResult{Features: []string{v1.SuspendFeature}}, nil
		},
		ExecuteWithRequest: func(_ context.Context, req ExecutionRequest) (*ExecutionResult, error) {
			mu.Lock()
			calls++
			call := calls
			mu.Unlock()

			if call == 1 {
				return &ExecutionResult{Suspend: &SuspendOutcome{
					CorrelationKey: "corr-1",
					WaitType:       WaitTypeSignal,
					ExpectedSignal: "approval",
				}}, nil
			}
			if req.Resume == nil {
				return &ExecutionResult{Error: "missing resume input"}, nil
			}
			return &ExecutionResult{Result: map[string]any{
				"cause":    req.Resume.Cause,
				"signal":   req.Resume.SignalName,
				"delivery": req.Resume.DeliveryID,
				"by":       req.Resume.Payload["by"],
				"logical":  req.LogicalOperationID,
				"attempt":  req.AttemptID,
			}}, nil
		},
	})

	initialize(t, client)

	suspended := execute(t, client, &v1.ExecuteRequest{
		LogicalOperationId: "logical-1",
		AttemptId:          "attempt-1",
	})
	if suspended.Suspend == nil {
		t.Fatalf("first invocation did not suspend: error=%q", suspended.Error)
	}

	resumed := execute(t, client, &v1.ExecuteRequest{
		LogicalOperationId: "logical-1",
		AttemptId:          "attempt-2",
		Resume: &v1.ResumeInput{
			Cause:      ResumeCauseSignal,
			SignalName: "approval",
			DeliveryId: "delivery-1",
			Payload:    map[string]*v1.Value{"by": stringValue("ada")},
		},
	})

	if resumed.Error != "" {
		t.Fatalf("resume error = %q", resumed.Error)
	}
	if got := resumed.Result["cause"].GetStringValue(); got != ResumeCauseSignal {
		t.Errorf("cause = %q, want %q", got, ResumeCauseSignal)
	}
	if got := resumed.Result["signal"].GetStringValue(); got != "approval" {
		t.Errorf("signal = %q, want %q", got, "approval")
	}
	if got := resumed.Result["delivery"].GetStringValue(); got != "delivery-1" {
		t.Errorf("delivery = %q, want %q", got, "delivery-1")
	}
	if got := resumed.Result["by"].GetStringValue(); got != "ada" {
		t.Errorf("payload by = %q, want %q", got, "ada")
	}
	if got := resumed.Result["logical"].GetStringValue(); got != "logical-1" {
		t.Errorf("logical operation = %q, want %q", got, "logical-1")
	}
	if got := resumed.Result["attempt"].GetStringValue(); got != "attempt-2" {
		t.Errorf("attempt = %q, want %q", got, "attempt-2")
	}
}

// TestConformanceTimerResume proves a timer suspend carries a positive
// deadline and is resumed by a fresh Execute with a timer cause.
func TestConformanceTimerResume(t *testing.T) {
	var (
		mu    sync.Mutex
		calls int
	)
	client := startConformanceServer(t, Handler{
		Initialize: func(context.Context, string, map[string]string) (*InitializeResult, error) {
			return &InitializeResult{Features: []string{v1.SuspendFeature}}, nil
		},
		ExecuteWithRequest: func(_ context.Context, req ExecutionRequest) (*ExecutionResult, error) {
			mu.Lock()
			calls++
			call := calls
			mu.Unlock()

			if call == 1 {
				return &ExecutionResult{Suspend: &SuspendOutcome{
					WaitType:       WaitTypeTimer,
					DeadlineUnixMs: 1720000000000,
				}}, nil
			}
			if req.Resume == nil {
				return &ExecutionResult{Error: "missing resume input"}, nil
			}
			return &ExecutionResult{Result: map[string]any{
				"cause": req.Resume.Cause,
			}}, nil
		},
	})

	initialize(t, client)

	suspended := execute(t, client, &v1.ExecuteRequest{LogicalOperationId: "timer-op"})
	if suspended.Suspend == nil {
		t.Fatalf("first invocation did not suspend: error=%q", suspended.Error)
	}
	if got := suspended.Suspend.WaitType; got != WaitTypeTimer {
		t.Errorf("wait_type = %q, want %q", got, WaitTypeTimer)
	}
	if suspended.Suspend.DeadlineUnixMs <= 0 {
		t.Errorf("deadline_unix_ms = %d, want a positive timer deadline", suspended.Suspend.DeadlineUnixMs)
	}

	resumed := execute(t, client, &v1.ExecuteRequest{
		LogicalOperationId: "timer-op",
		Resume: &v1.ResumeInput{
			Cause: ResumeCauseTimer,
		},
	})
	if resumed.Error != "" {
		t.Fatalf("resume error = %q", resumed.Error)
	}
	if got := resumed.Result["cause"].GetStringValue(); got != ResumeCauseTimer {
		t.Errorf("cause = %q, want %q", got, ResumeCauseTimer)
	}
}

// TestConformanceDuplicateResumeDelivery proves the SDK delivers a duplicate
// resume (same delivery ID) each time. At-least-once delivery is why the
// contract exposes delivery_id: the capability dedupes on it.
func TestConformanceDuplicateResumeDelivery(t *testing.T) {
	var (
		mu   sync.Mutex
		seen = make(map[string]int)
	)
	client := startConformanceServer(t, Handler{
		Initialize: func(context.Context, string, map[string]string) (*InitializeResult, error) {
			return &InitializeResult{Features: []string{v1.SuspendFeature}}, nil
		},
		ExecuteWithRequest: func(_ context.Context, req ExecutionRequest) (*ExecutionResult, error) {
			if req.Resume == nil {
				return &ExecutionResult{Error: "missing resume input"}, nil
			}
			mu.Lock()
			seen[req.Resume.DeliveryID]++
			count := seen[req.Resume.DeliveryID]
			mu.Unlock()
			return &ExecutionResult{Result: map[string]any{
				"delivery": req.Resume.DeliveryID,
				"count":    count,
				"logical":  req.LogicalOperationID,
			}}, nil
		},
	})

	initialize(t, client)

	request := func(attempt string) *v1.ExecuteRequest {
		return &v1.ExecuteRequest{
			LogicalOperationId: "dup-op",
			AttemptId:          attempt,
			Resume: &v1.ResumeInput{
				Cause:      ResumeCauseSignal,
				SignalName: "approval",
				DeliveryId: "duplicate-1",
				Payload:    map[string]*v1.Value{"by": stringValue("ada")},
			},
		}
	}

	first := execute(t, client, request("attempt-1"))
	second := execute(t, client, request("attempt-2"))

	for i, resp := range []*v1.ExecuteResponse{first, second} {
		if resp.Error != "" {
			t.Fatalf("delivery %d error = %q", i+1, resp.Error)
		}
		if got := resp.Result["delivery"].GetStringValue(); got != "duplicate-1" {
			t.Errorf("delivery %d delivery_id = %q, want %q", i+1, got, "duplicate-1")
		}
		if got := resp.Result["logical"].GetStringValue(); got != "dup-op" {
			t.Errorf("delivery %d logical operation = %q, want %q", i+1, got, "dup-op")
		}
	}
	if got := first.Result["count"].GetNumberValue(); got != 1 {
		t.Errorf("first delivery count = %v, want 1", got)
	}
	if got := second.Result["count"].GetNumberValue(); got != 2 {
		t.Errorf("second delivery count = %v, want 2; duplicate was dropped", got)
	}
}

// TestConformanceLegacyExecuteResultUnchanged proves the legacy handler surface
// still returns a plain result with no suspend field.
func TestConformanceLegacyExecuteResultUnchanged(t *testing.T) {
	client := startConformanceServer(t, Handler{
		Initialize: func(context.Context, string, map[string]string) (*InitializeResult, error) {
			return &InitializeResult{Features: []string{v1.SuspendFeature}}, nil
		},
		Execute: func(_ context.Context, params map[string]any) (map[string]any, error) {
			return map[string]any{"value": params["value"]}, nil
		},
	})

	initialize(t, client)

	resp := execute(t, client, &v1.ExecuteRequest{
		Params: map[string]*v1.Value{"value": stringValue("legacy")},
	})
	if resp.Suspend != nil {
		t.Fatalf("suspend = %v, want nil from a legacy handler", resp.Suspend)
	}
	if resp.Error != "" {
		t.Fatalf("error = %q, want empty", resp.Error)
	}
	if got := resp.Result["value"].GetStringValue(); got != "legacy" {
		t.Errorf("value = %q, want %q", got, "legacy")
	}
}

// TestExecuteStdioRejectsSuspend proves the legacy stdin/stdout JSON transport
// reports a suspend as an explicit failure rather than dropping it.
func TestExecuteStdioRejectsSuspend(t *testing.T) {
	handler := Handler{
		Initialize: func(context.Context, string, map[string]string) (*InitializeResult, error) {
			return &InitializeResult{Features: []string{v1.SuspendFeature}}, nil
		},
		ExecuteWithRequest: func(context.Context, ExecutionRequest) (*ExecutionResult, error) {
			return &ExecutionResult{Suspend: &SuspendOutcome{
				WaitType:       WaitTypeSignal,
				ExpectedSignal: "approval",
			}}, nil
		},
	}

	resp := executeStdio(context.Background(), handler, &capabilityrt.Request{Params: map[string]any{}})
	if !strings.Contains(resp.Error, "unsupported_suspend_outcome") {
		t.Errorf("error = %q, want an unsupported_suspend_outcome failure", resp.Error)
	}
	if len(resp.Result) != 0 {
		t.Errorf("result = %v, want empty on rejection", resp.Result)
	}
}
