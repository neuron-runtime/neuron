// Package capability runtime provides the Go SDK for implementing Neuron capability runtimes.
//
// CapabilityRuntime authors implement the Handler interface and call Serve to start
// their capability runtime. The SDK handles protocol negotiation, transport setup, and
// lifecycle management.
//
// Example:
//
//	func main() {
//		err := capabilityruntime.Serve(capabilityruntime.Handler{
//			Initialize: func(ctx context.Context, protocol string, metadata map[string]string) (*capabilityruntime.InitializeResult, error) {
//				return &capabilityruntime.InitializeResult{ProtocolVersion: capabilityrt.ProtocolV1}, nil
//			},
//			Execute: func(ctx context.Context, in map[string]any) (map[string]any, error) {
//				return in, nil
//			},
//		})
//		if err != nil {
//			os.Exit(1)
//		}
//	}
//
// The SDK supports two transports:
//
//  1. gRPC over Unix domain sockets (when NEURON_CAPABILITY_RUNTIME_SOCKET is set —
//     the long-lived worker mode used by the process runtime backend)
//  2. stdin/stdout JSON (when NEURON_CAPABILITY_RUNTIME_SOCKET is not set — the
//     one-shot mode used by WASM capability runtimes and single-execution processes)
//
// The transport is selected automatically based on the environment provided
// by N.O.R.E. This means the same capability runtime binary works both as a long-lived
// gRPC worker and as a one-shot stdin/stdout command.
package capabilityruntime

import (
	"context"
	"fmt"
	"os"
	"sync"

	v1 "github.com/neuron-runtime/neuron/shared/protocol/capabilityruntime/v1"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	"google.golang.org/grpc"
)

// Handler implements the business logic of a capability runtime. It is the contract
// that capability runtime authors implement. The SDK wraps it with the gRPC transport
// and lifecycle management.
type Handler struct {
	// Initialize is called once per process lifetime after the gRPC connection
	// is established. It negotiates the protocol version and returns the
	// capability runtime's capabilities.
	Initialize func(ctx context.Context, protocol string, metadata map[string]string) (*InitializeResult, error)

	// Execute runs one execution and returns the result. The params are the
	// resolved invocation data for the capability. Returning an error from
	// Execute records a controlled failure.
	Execute func(ctx context.Context, params map[string]any) (map[string]any, error)

	// ExecuteWithRequest, when set, supersedes Execute. It receives the full
	// request, including the resume input of a suspended capability, and may
	// return an ExecutionResult whose Suspend field asks N.O.R.E. to suspend
	// the capability.
	//
	// A handler that returns Suspend must advertise v1.SuspendFeature
	// ("suspend") from Initialize. The SDK rejects an unnegotiated suspend
	// explicitly rather than returning an empty success.
	ExecuteWithRequest func(ctx context.Context, req ExecutionRequest) (*ExecutionResult, error)

	// Health reports whether the capability runtime is ready to accept requests.
	// It is called periodically by the runtime.
	Health func(ctx context.Context) error

	// Shutdown is called before the process terminates. It should complete
	// any graceful cleanup within the timeout.
	Shutdown func(ctx context.Context) error
}

// InitializeResult carries the capability runtime's response to protocol negotiation.
type InitializeResult struct {
	// ProtocolVersion is the protocol version the capability runtime supports.
	ProtocolVersion string

	// Features declared by the capability runtime.
	Features []string

	// Metadata carries capability runtime-specific identity information.
	Metadata map[string]string
}

// Serve starts the capability runtime. The transport is selected automatically:
//
//   - if NEURON_CAPABILITY_RUNTIME_SOCKET is set, the capability runtime starts a gRPC server
//     listening on the Unix domain socket and signals readiness by writing
//     the NEURON_CAPABILITY_RUNTIME_READY file
//   - otherwise, the capability runtime runs in stdin/stdout JSON mode
//
// Serve blocks until the capability runtime is shut down (either via gRPC Shutdown or
// process termination). It never returns nil; an error is always returned,
// or a sentinel error indicating normal shutdown.
func Serve(h Handler) error {
	if h.Initialize == nil || (h.Execute == nil && h.ExecuteWithRequest == nil) {
		return fmt.Errorf("capability runtime: Handler.Initialize and one of Handler.Execute or Handler.ExecuteWithRequest are required")
	}

	if socketPath := os.Getenv("NEURON_CAPABILITY_RUNTIME_SOCKET"); socketPath != "" {
		return serveGRPC(socketPath, h)
	}

	return serveStdio(h)
}

// serveGRPC starts the capability runtime as a gRPC server on a Unix domain socket.
func serveGRPC(socketPath string, h Handler) error {
	listener, err := newSocketListener(socketPath)
	if err != nil {
		return fmt.Errorf("capability runtime: open listener: %w", err)
	}

	server := newGRPCServer(h)
	srv := grpc.NewServer()
	grpcShutdownCh := make(chan struct{}, 1)
	server.onShutdown = func() {
		grpcShutdownCh <- struct{}{}
	}
	v1.RegisterCapabilityRuntimeServiceServer(srv, server)

	// Signal readiness so N.O.R.E. can connect.
	if readyFile := os.Getenv("NEURON_CAPABILITY_RUNTIME_READY"); readyFile != "" {
		if err := touchFile(readyFile); err != nil {
			listener.Close()
			return fmt.Errorf("capability runtime: signal readiness: %w", err)
		}
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(listener)
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("capability runtime: serve: %w", err)
	case <-grpcShutdownCh:
		srv.GracefulStop()
		return nil
	}
}

// serveStdio runs the capability runtime in stdin/stdout JSON mode. This is the
// one-shot protocol used by WASM capability runtimes and legacy process capability runtimes.
func serveStdio(h Handler) error {
	req, err := readRequestFromStdin()
	if err != nil {
		writeStdioError(fmt.Sprintf("invalid request: %v", err))
		return err
	}

	resp := executeStdio(context.Background(), h, req)
	if resp.Error != "" {
		writeStdioError(resp.Error)
		return nil
	}

	return writeStdioOutput(resp.Result)
}

// handlerOutcome is a normalized execution outcome. errText carries a
// controlled failure from an outcome-aware handler; it is returned over the
// transport rather than raised as a transport error.
type handlerOutcome struct {
	result  map[string]any
	errText string
	suspend *SuspendOutcome
}

// invokeHandler calls the configured execution entry point and normalizes its
// outcome. ExecuteWithRequest supersedes Execute when present.
func invokeHandler(ctx context.Context, h Handler, req ExecutionRequest) (handlerOutcome, error) {
	if h.ExecuteWithRequest != nil {
		result, err := h.ExecuteWithRequest(ctx, req)
		if err != nil {
			return handlerOutcome{}, err
		}
		if result == nil {
			return handlerOutcome{}, nil
		}
		return handlerOutcome{
			result:  result.Result,
			errText: result.Error,
			suspend: result.Suspend,
		}, nil
	}

	result, err := h.Execute(ctx, req.Params)
	if err != nil {
		return handlerOutcome{}, err
	}
	return handlerOutcome{result: result}, nil
}

// validateSuspendOutcome rejects invalid or conflicting suspend outcomes
// explicitly. A non-empty message describes the failure.
func validateSuspendOutcome(outcome handlerOutcome) string {
	if outcome.suspend == nil {
		return ""
	}
	if len(outcome.result) != 0 || outcome.errText != "" {
		return "unsupported_suspend_outcome: suspend must not be combined with a non-empty result or error"
	}

	switch outcome.suspend.WaitType {
	case WaitTypeSignal:
		if outcome.suspend.ExpectedSignal == "" {
			return "unsupported_suspend_outcome: signal wait requires a non-empty expected_signal"
		}
	case WaitTypeTimer:
		if outcome.suspend.DeadlineUnixMs <= 0 {
			return "unsupported_suspend_outcome: timer wait requires deadline_unix_ms > 0"
		}
	default:
		return "unsupported_suspend_outcome: wait_type must be \"signal\" or \"timer\""
	}

	return ""
}

// sdkServer is the internal gRPC server implementation that adapts the
// Handler to the protobuf capability contract.
type sdkServer struct {
	v1.UnimplementedCapabilityRuntimeServiceServer
	handler    Handler
	onShutdown func()

	mu       sync.RWMutex
	features map[string]struct{}
}

func newGRPCServer(h Handler) *sdkServer {
	return &sdkServer{handler: h}
}

// setFeatures records the features negotiated by the Initialize handshake. A
// later Execute uses them to decide whether a suspend outcome is allowed.
// Features are monotonic across repeated Initialize calls so a previously
// advertised capability (such as suspend) is not revoked.
func (s *sdkServer) setFeatures(features []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.features == nil {
		s.features = make(map[string]struct{}, len(features))
	}
	for _, feature := range features {
		s.features[feature] = struct{}{}
	}
}

// supportsSuspend reports whether the runtime advertised the suspend feature.
func (s *sdkServer) supportsSuspend() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.features[v1.SuspendFeature]
	return ok
}

// Initialize handles the protocol negotiation handshake.
func (s *sdkServer) Initialize(ctx context.Context, req *v1.InitializeRequest) (*v1.InitializeResponse, error) {
	result, err := s.handler.Initialize(ctx, req.ProtocolVersion, req.Metadata)
	if err != nil {
		return nil, err
	}

	response := &v1.InitializeResponse{}
	if result != nil {
		response.ProtocolVersion = result.ProtocolVersion
		response.Features = result.Features
		response.Metadata = result.Metadata
	}

	// Default protocol version if not specified.
	if response.ProtocolVersion == "" {
		response.ProtocolVersion = capabilityrt.ProtocolV1
	}

	s.setFeatures(response.Features)

	return response, nil
}

// Execute adapts a protobuf ExecuteRequest to the Handler.
func (s *sdkServer) Execute(ctx context.Context, req *v1.ExecuteRequest) (*v1.ExecuteResponse, error) {
	params := make(map[string]any)
	for k, v := range req.Params {
		params[k] = fromProtoValue(v)
	}

	outcome, err := invokeHandler(ctx, s.handler, ExecutionRequest{
		ExecutionID:        req.ExecutionId,
		Params:             params,
		TimeoutMs:          req.TimeoutMs,
		CorrelationID:      req.CorrelationId,
		AttemptID:          req.AttemptId,
		LogicalOperationID: req.LogicalOperationId,
		Resume:             resumeInputFromProto(req.Resume),
	})
	if err != nil {
		return &v1.ExecuteResponse{
			Error: err.Error(),
		}, nil
	}

	if msg := validateSuspendOutcome(outcome); msg != "" {
		return &v1.ExecuteResponse{Error: msg}, nil
	}

	if outcome.errText != "" {
		return &v1.ExecuteResponse{
			Error: outcome.errText,
		}, nil
	}

	if outcome.suspend != nil {
		if !s.supportsSuspend() {
			return &v1.ExecuteResponse{
				Error: "unsupported_suspend_outcome: capability runtime did not advertise the \"suspend\" feature during Initialize",
			}, nil
		}
		return &v1.ExecuteResponse{
			Suspend: suspendOutcomeToProto(outcome.suspend),
		}, nil
	}

	resp := &v1.ExecuteResponse{
		Result: make(map[string]*v1.Value, len(outcome.result)),
	}
	for k, v := range outcome.result {
		resp.Result[k] = toProtoValue(v)
	}

	return resp, nil
}

// Health reports capability runtime health.
func (s *sdkServer) Health(ctx context.Context, req *v1.HealthRequest) (*v1.HealthResponse, error) {
	if s.handler.Health != nil {
		if err := s.handler.Health(ctx); err != nil {
			return &v1.HealthResponse{Ready: false, Message: err.Error()}, nil
		}
	}
	return &v1.HealthResponse{Ready: true}, nil
}

// Shutdown triggers graceful termination.
func (s *sdkServer) Shutdown(ctx context.Context, req *v1.ShutdownRequest) (*v1.ShutdownResponse, error) {
	if s.handler.Shutdown != nil {
		if err := s.handler.Shutdown(ctx); err != nil {
			return nil, err
		}
	}
	if s.onShutdown != nil {
		s.onShutdown()
	}
	return &v1.ShutdownResponse{}, nil
}
