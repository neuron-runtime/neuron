package api

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/Muhammad-Jay/neuron/nore/internal/api/routes"
	"github.com/Muhammad-Jay/neuron/nore/internal/instance"
	"github.com/Muhammad-Jay/neuron/nore/internal/planner"
	"github.com/Muhammad-Jay/neuron/nore/internal/assembly"
	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
)

type Server struct {
	mux       *http.ServeMux
	instances *instance.Manager
}

func NewServer(inst *instance.Manager, assemblies *assembly.Repository, compiler *planner.Compiler) *Server {
	s := &Server{
		mux:       http.NewServeMux(),
		instances: inst,
	}

	routes.BuildRoutes(s.mux, s.instances, assemblies, compiler)

	return s
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	httpServer := &http.Server{
		Handler:           s.mux,
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
