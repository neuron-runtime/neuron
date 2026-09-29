package routes

import (
	"net/http"

	"github.com/neuron-runtime/neuron/nore/internal/api/bridge"
	"github.com/neuron-runtime/neuron/nore/internal/api/handlers/health"
	"github.com/neuron-runtime/neuron/nore/internal/api/handlers/instances"
	"github.com/neuron-runtime/neuron/nore/internal/api/handlers/register"
	"github.com/neuron-runtime/neuron/nore/internal/api/middleware"
	"github.com/neuron-runtime/neuron/nore/internal/api/websocket"
	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	"github.com/neuron-runtime/neuron/nore/internal/instance"
	"github.com/neuron-runtime/neuron/nore/internal/planner"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// BuildRoutes registers every API route on mux and returns the wrapped request
// chain that must actually be served.
//
// Middleware order is deliberate. Recovery is outermost so it also covers
// panics raised by the layers inside it. Authentication follows, so an
// unauthenticated request is rejected before it reaches logging-heavy or
// handler code. Logging is innermost so it records requests that were actually
// accepted, keeping rejected probes out of the access log.
func BuildRoutes(
	mux *http.ServeMux,
	mgr *instance.Manager,
	assemblies *assembly.Repository,
	compiler *planner.Compiler,
	token string,
) http.Handler {
	// Health
	mux.HandleFunc("GET /health", health.Health)

	// Instances
	instHandler := instances.New(mgr, assemblies, compiler)
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
	reg := register.New(mgr, assemblies, compiler)
	mux.HandleFunc("POST /v1/register", reg.Register)

	handler := middleware.Logging(mux)
	handler = middleware.NewTokenAuth(token).Wrap(handler)

	return middleware.Recovery(handler)
}
