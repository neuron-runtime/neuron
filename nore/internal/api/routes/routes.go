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
	// Every route is registered from the shared route table rather than from a
	// literal spelled out here. The table is the same one the CLI builds its URLs
	// from, so a route cannot be renamed on one side and left behind on the
	// other: there is nothing left here to disagree with.

	// Health
	mux.HandleFunc(protocol.Method(http.MethodGet, protocol.HealthPath), health.Health)

	// Instances
	instHandler := instances.New(mgr, assemblies, compiler)
	mux.HandleFunc(protocol.Method(http.MethodGet, protocol.InstancesPath), instHandler.ListInstances)
	mux.HandleFunc(protocol.Method(http.MethodPost, protocol.InstancesPath), instHandler.CreateInstance)
	mux.HandleFunc(protocol.Method(http.MethodDelete, protocol.InstancesPath), instHandler.ClearInstances)
	mux.HandleFunc(protocol.Method(http.MethodGet, protocol.InstanceByIDPath), instHandler.GetInstanceByID)
	mux.HandleFunc(protocol.Method(http.MethodDelete, protocol.InstanceByIDPath), instHandler.RemoveInstance)

	// Executions
	mux.HandleFunc(protocol.Method(http.MethodPost, protocol.ExecutePath), instHandler.Execute)
	mux.HandleFunc(protocol.Method(http.MethodGet, protocol.ExecutePath), instHandler.ListExecutions)
	mux.HandleFunc(protocol.Method(http.MethodGet, protocol.ExecutionByIDPath), instHandler.GetExecutionState)
	mux.HandleFunc(protocol.Method(http.MethodGet, protocol.ExecutionEventsPath), instHandler.GetExecutionEvents)
	mux.HandleFunc(protocol.Method(http.MethodGet, protocol.ExecutionEventsStreamPath), instHandler.StreamExecutionEvents)
	mux.HandleFunc(protocol.Method(http.MethodPost, protocol.CancelExecutionPath), instHandler.CancelExecution)

	// WebSocket
	ws := websocket.NewWebSocketHandler()
	ws.SetRoomProvider(bridge.New(mgr))
	mux.HandleFunc(protocol.WebSocketPath, ws.HandleWebSocket)

	// Register
	reg := register.New(mgr, assemblies, compiler)
	mux.HandleFunc(protocol.Method(http.MethodPost, protocol.RegisterPath), reg.Register)

	handler := middleware.Logging(mux)
	handler = middleware.NewTokenAuth(token).Wrap(handler)

	return middleware.Recovery(handler)
}
