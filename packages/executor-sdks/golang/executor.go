// Package executor provides the Go SDK for implementing Neuron executors.
//
// Executor authors implement the Handler interface and call Serve to start
// their executor. The SDK handles protocol negotiation, transport setup, and
// lifecycle management.
//
// Example:
//
//	func main() {
//		err := executor.Serve(executor.Handler{
//			Initialize: func(ctx context.Context, protocol string, metadata map[string]string) (*executor.InitializeResult, error) {
//				return &executor.InitializeResult{ProtocolVersion: shadexec.ProtocolV1}, nil
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
//  1. gRPC over Unix domain sockets (when NEURON_EXECUTOR_SOCKET is set —
//     the long-lived worker mode used by the process runtime backend)
//  2. stdin/stdout JSON (when NEURON_EXECUTOR_SOCKET is not set — the
//     one-shot mode used by WASM executors and single-execution processes)
//
// The transport is selected automatically based on the environment provided
// by N.O.R.E. This means the same executor binary works both as a long-lived
// gRPC worker and as a one-shot stdin/stdout command.
package executor

import (
	"context"
	"fmt"
	"os"

	v1 "github.com/Muhammad-Jay/neuron/shared/protocol/executor/v1"
	shadexec "github.com/Muhammad-Jay/neuron/shared/types/executor"
	"google.golang.org/grpc"
)

// Handler implements the business logic of an executor. It is the contract
// that executor authors implement. The SDK wraps it with the gRPC transport
// and lifecycle management.
type Handler struct {
	// Initialize is called once per process lifetime after the gRPC connection
	// is established. It negotiates the protocol version and returns the
	// executor's capabilities.
	Initialize func(ctx context.Context, protocol string, metadata map[string]string) (*InitializeResult, error)

	// Execute runs one execution and returns the output. The input is the
	// resolved execution input for the service. Returning an error from
	// Execute records a controlled failure.
	Execute func(ctx context.Context, input map[string]any) (map[string]any, error)

	// Health reports whether the executor is ready to accept requests.
	// It is called periodically by the runtime.
	Health func(ctx context.Context) error

	// Shutdown is called before the process terminates. It should complete
	// any graceful cleanup within the timeout.
	Shutdown func(ctx context.Context) error
}

// InitializeResult carries the executor's response to protocol negotiation.
type InitializeResult struct {
	// ProtocolVersion is the protocol version the executor supports.
	ProtocolVersion string

	// Capabilities declared by the executor.
	Capabilities []string

	// Metadata carries executor-specific identity information.
	Metadata map[string]string
}

// Serve starts the executor. The transport is selected automatically:
//
//   - if NEURON_EXECUTOR_SOCKET is set, the executor starts a gRPC server
//     listening on the Unix domain socket and signals readiness by writing
//     the NEURON_EXECUTOR_READY file
//   - otherwise, the executor runs in stdin/stdout JSON mode
//
// Serve blocks until the executor is shut down (either via gRPC Shutdown or
// process termination). It never returns nil; an error is always returned,
// or a sentinel error indicating normal shutdown.
func Serve(h Handler) error {
	if h.Initialize == nil || h.Execute == nil {
		return fmt.Errorf("executor: Handler.Initialize and Handler.Execute are required")
	}

	if socketPath := os.Getenv("NEURON_EXECUTOR_SOCKET"); socketPath != "" {
		return serveGRPC(socketPath, h)
	}

	return serveStdio(h)
}

// serveGRPC starts the executor as a gRPC server on a Unix domain socket.
func serveGRPC(socketPath string, h Handler) error {
	listener, err := newSocketListener(socketPath)
	if err != nil {
		return fmt.Errorf("executor: open listener: %w", err)
	}

	server := newGRPCServer(h)
	srv := grpc.NewServer()
	grpcShutdownCh := make(chan struct{}, 1)
	server.onShutdown = func() {
		grpcShutdownCh <- struct{}{}
	}
	v1.RegisterExecutorServiceServer(srv, server)

	// Signal readiness so N.O.R.E. can connect.
	if readyFile := os.Getenv("NEURON_EXECUTOR_READY"); readyFile != "" {
		if err := touchFile(readyFile); err != nil {
			listener.Close()
			return fmt.Errorf("executor: signal readiness: %w", err)
		}
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(listener)
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("executor: serve: %w", err)
	case <-grpcShutdownCh:
		srv.GracefulStop()
		return nil
	}
}

// serveStdio runs the executor in stdin/stdout JSON mode. This is the
// one-shot protocol used by WASM executors and legacy process executors.
func serveStdio(h Handler) error {
	req, err := readRequestFromStdin()
	if err != nil {
		writeStdioError(fmt.Sprintf("invalid request: %v", err))
		return err
	}

	ctx := context.Background()
	out, err := h.Execute(ctx, req.Input)
	if err != nil {
		writeStdioError(err.Error())
		return nil
	}

	return writeStdioOutput(out)
}

// sdkServer is the internal gRPC server implementation that adapts the
// Handler to the protobuf service contract.
type sdkServer struct {
	v1.UnimplementedExecutorServiceServer
	handler    Handler
	onShutdown func()
}

func newGRPCServer(h Handler) *sdkServer {
	return &sdkServer{handler: h}
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
		response.Capabilities = result.Capabilities
		response.Metadata = result.Metadata
	}

	// Default protocol version if not specified.
	if response.ProtocolVersion == "" {
		response.ProtocolVersion = shadexec.ProtocolV1
	}

	return response, nil
}

// Execute adapts a protobuf ExecuteRequest to the Handler.
func (s *sdkServer) Execute(ctx context.Context, req *v1.ExecuteRequest) (*v1.ExecuteResponse, error) {
	input := make(map[string]any)
	for k, v := range req.Input {
		input[k] = fromProtoValue(v)
	}

	out, err := s.handler.Execute(ctx, input)
	if err != nil {
		return &v1.ExecuteResponse{
			Error: err.Error(),
		}, nil
	}

	resp := &v1.ExecuteResponse{
		Output: make(map[string]*v1.Value, len(out)),
	}
	for k, v := range out {
		resp.Output[k] = toProtoValue(v)
	}

	return resp, nil
}

// Health reports executor health.
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
