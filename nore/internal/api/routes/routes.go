package routes

import (
	"net/http"

	"github.com/Muhammad-Jay/neuron/nore/internal/api/bridge"
	"github.com/Muhammad-Jay/neuron/nore/internal/api/handlers/health"
	"github.com/Muhammad-Jay/neuron/nore/internal/api/handlers/instances"
	"github.com/Muhammad-Jay/neuron/nore/internal/api/handlers/register"
	"github.com/Muhammad-Jay/neuron/nore/internal/api/middleware"
	"github.com/Muhammad-Jay/neuron/nore/internal/api/websocket"
	"github.com/Muhammad-Jay/neuron/nore/internal/instance"
	"github.com/Muhammad-Jay/neuron/nore/internal/planner"
	"github.com/Muhammad-Jay/neuron/nore/internal/system"
	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
)

func BuildRoutes(mux *http.ServeMux, mgr *instance.Manager, systems *system.Repository, compiler *planner.Compiler) http.Handler {
	// Health
	mux.HandleFunc("GET /health", health.Health)

	// Instances
	instHandler := instances.New(mgr, systems, compiler)
	mux.HandleFunc("GET /v1/instances", instHandler.ListInstances)
	mux.HandleFunc("POST /v1/instances", instHandler.CreateInstance)
	mux.HandleFunc("DELETE /v1/instances", instHandler.ClearInstances)
	mux.HandleFunc("GET /v1/instances/{id}", instHandler.GetInstanceByID)
	mux.HandleFunc("DELETE /v1/instances/{id}", instHandler.RemoveInstance)

	// Executions
	mux.HandleFunc("POST /v1/instances/{id}/executions", instHandler.Execute)
	mux.HandleFunc("GET /v1/instances/{id}/executions", instHandler.ListExecutions)
	mux.HandleFunc("GET /v1/instances/{id}/executions/{execID}", instHandler.GetExecutionState)
	mux.HandleFunc("GET /v1/instances/{id}/executions/{execID}/events", instHandler.GetExecutionEvents)
	mux.HandleFunc("GET /v1/instances/{id}/executions/{execID}/events/stream", instHandler.StreamExecutionEvents)

	// WebSocket
	ws := websocket.NewWebSocketHandler()
	ws.SetRoomProvider(bridge.New(mgr))
	mux.HandleFunc(protocol.WebSocketPath, ws.HandleWebSocket)

	// Register
	reg := register.New(mgr, systems, compiler)
	mux.HandleFunc("POST /v1/register", reg.Register)

	handler := middleware.Recovery(mux)
	handler = middleware.Logging(handler)

	return handler
}
