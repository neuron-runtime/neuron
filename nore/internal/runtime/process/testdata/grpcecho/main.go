// Command grpcecho is a gRPC executor used to exercise the process runtime's
// long-lived worker pool over Unix domain sockets. It is built only for
// tests (see nore/internal/runtime/process/runtime_test.go). It implements
// the CapabilityRuntimeService contract directly against the shared protobuf types.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"

	v1 "github.com/Muhammad-Jay/neuron/shared/protocol/capabilityruntime/v1"
	"google.golang.org/grpc"
)

var socket = flag.String("socket", "", "unix socket path")

type server struct {
	v1.UnimplementedCapabilityRuntimeServiceServer
	typeName string
}

func (s *server) Initialize(ctx context.Context, req *v1.InitializeRequest) (*v1.InitializeResponse, error) {
	return &v1.InitializeResponse{
		ProtocolVersion: req.ProtocolVersion,
		Features:        []string{},
	}, nil
}

func (s *server) Execute(ctx context.Context, req *v1.ExecuteRequest) (*v1.ExecuteResponse, error) {
	out := make(map[string]*v1.Value, len(req.Params)+3)
	for k, v := range req.Params {
		out[k] = v
	}
	out["type"] = toValue(s.typeName)
	out["protocol"] = toValue("grpc-test")
	out["version"] = toValue("1.0.0")
	return &v1.ExecuteResponse{Result: out}, nil
}

func (s *server) Health(ctx context.Context, req *v1.HealthRequest) (*v1.HealthResponse, error) {
	return &v1.HealthResponse{Ready: true}, nil
}

func (s *server) Shutdown(ctx context.Context, req *v1.ShutdownRequest) (*v1.ShutdownResponse, error) {
	return &v1.ShutdownResponse{}, nil
}

func toValue(v string) *v1.Value {
	return &v1.Value{Kind: &v1.Value_StringValue{StringValue: v}}
}

func main() {
	flag.Parse()
	addr := *socket
	if addr == "" {
		addr = os.Getenv("NEURON_EXECUTOR_SOCKET")
	}
	if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
		log.Fatalf("remove socket: %v", err)
	}
	ln, err := net.Listen("unix", addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	if ready := os.Getenv("NEURON_EXECUTOR_READY"); ready != "" {
		if err := os.WriteFile(ready, []byte("ready"), 0o644); err != nil {
			log.Fatalf("write ready: %v", err)
		}
	}
	srv := grpc.NewServer()
	v1.RegisterCapabilityRuntimeServiceServer(srv, &server{typeName: os.Getenv("NEURON_EXECUTOR_TYPE")})
	fmt.Fprintln(os.Stderr, "grpcecho serving on", addr)
	if err := srv.Serve(ln); err != nil {
		log.Fatal(err)
	}
}
