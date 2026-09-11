// Package bridge connects the WebSocket transport to per-instance execution
// event streams. It implements the websocket.RoomProvider contract so that a
// client subscribing to an execution room receives an ordered history-plus-live
// event stream, mirroring the SSE event endpoint over WebSocket.
package bridge

import (
	"context"
	"fmt"
	"strings"

	"github.com/Muhammad-Jay/neuron/nore/internal/instance"
	"github.com/Muhammad-Jay/neuron/shared/protocol/websocket"
	"github.com/Muhammad-Jay/neuron/shared/types/core"
	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
)

const (
	roomSegmentCount  = 3
	roomExecInstance  = 1
	roomExecExecution = 2
)

// EventBridge resolves execution rooms to per-subscriber event streams. It is
// instantiated once per API server and is stateless between subscriptions.
type EventBridge struct {
	instances *instance.Manager
}

// New creates an EventBridge backed by the live instance manager.
func New(instances *instance.Manager) *EventBridge {
	return &EventBridge{instances: instances}
}

// ExecutionRoom returns the WebSocket room address for an execution. The name
// is defined by the shared wire protocol so client and server agree.
func ExecutionRoom(instanceID string, executionID core.ID) string {
	return websocket.ExecutionRoom(instanceID, string(executionID))
}

// Stream returns an ordered stream of execution events (persisted history
// followed by live bus events) for the execution addressed by room. Each
// subscriber receives an independent stream with exactly-once semantics. The
// returned channel is closed when ctx ends or the execution stream ends.
func (b *EventBridge) Stream(ctx context.Context, room string) (<-chan protocol.StreamEvent, error) {
	instanceID, executionID, err := splitExecutionRoom(room)
	if err != nil {
		return nil, err
	}

	if b.instances == nil {
		return nil, fmt.Errorf("event bridge is not initialized")
	}

	inst, ok := b.instances.GetByID(instanceID)
	if !ok {
		return nil, fmt.Errorf("instance %s not found", instanceID)
	}
	if _, ok := inst.GetExecution(executionID); !ok {
		return nil, fmt.Errorf("execution %s not found", executionID)
	}

	messages, err := inst.Events(ctx, executionID, "")
	if err != nil {
		return nil, err
	}

	out := make(chan protocol.StreamEvent, 256)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-messages:
				if !ok {
					return
				}
				evt := protocol.StreamEvent{
					ID:            msg.EventID,
					Type:          msg.Type.String(),
					CorrelationID: msg.CorrelationID,
					ServiceID:     msg.ServiceID,
					OccurredAt:    msg.OccurredAt.UnixNano(),
					Payload:       msg.Payload,
				}
				select {
				case out <- evt:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, nil
}

// splitExecutionRoom parses "exec:{instanceID}:{executionID}".
func splitExecutionRoom(room string) (instanceID string, executionID core.ID, err error) {
	parts := strings.Split(room, ":")
	if len(parts) != roomSegmentCount || parts[0] != websocket.RoomPrefixExecution {
		return "", "", fmt.Errorf("room %q is not an execution room", room)
	}
	if parts[roomExecInstance] == "" || parts[roomExecExecution] == "" {
		return "", "", fmt.Errorf("room %q is not an execution room", room)
	}
	return parts[roomExecInstance], core.ID(parts[roomExecExecution]), nil
}
