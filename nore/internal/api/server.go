package api

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/api/routes"
	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	"github.com/neuron-runtime/neuron/nore/internal/instance"
	"github.com/neuron-runtime/neuron/nore/internal/planner"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

type Server struct {
	// handler is the fully wrapped request chain built by routes.BuildRoutes,
	// including logging, panic recovery, and token authentication. The bare mux
	// is not served: the middleware is part of the server's contract, not an
	// optional layer.
	handler   http.Handler
	mux       *http.ServeMux
	instances *instance.Manager
}

// NewServer builds a server. The token authenticates every API request that is
// not the health probe; an empty token leaves the API unauthenticated, which
// the daemon permits only for a socket-scoped listener and refuses for TCP.
func NewServer(inst *instance.Manager, assemblies *assembly.Repository, compiler *planner.Compiler, token string) *Server {
	s := &Server{
		mux:       http.NewServeMux(),
		instances: inst,
	}

	s.handler = routes.BuildRoutes(s.mux, s.instances, assemblies, compiler, token)

	return s
}

// Handler returns the wrapped request chain, so an in-process consumer sees the
// same logging, recovery, and authentication as a network client.
func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	httpServer := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	return httpServer.Serve(listener)
}

func (s *Server) StopInstances() {
	for _, i := range s.instances.List(protocol.ListOptions{}) {
		_ = i.Stop()
	}
}
