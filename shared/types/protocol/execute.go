package protocol

import (
	"encoding/json"

	"github.com/Muhammad-Jay/neuron/shared/types/core"
)

type ExecutionItem struct {
	ID            core.ID `json:"id"`
	CorrelationID core.ID `json:"correlation_id"`
	Status        string  `json:"status"`
	StartedAt     *int64  `json:"started_at,omitempty"`
	CompletedAt   *int64  `json:"completed_at,omitempty"`
	Error         string  `json:"error,omitempty"`
}

type EventItem struct {
	ID           core.ID `json:"id"`
	Type         string  `json:"type"`
	CapabilityID core.ID `json:"capability_id"`
	Payload      any     `json:"payload"`
}

// StreamEvent is the canonical wire shape for both historical replay and live
// stream events emitted over the SSE endpoint.
type StreamEvent struct {
	ID            core.ID         `json:"id"`
	Type          string          `json:"type"`
	CorrelationID core.ID         `json:"correlation_id,omitempty"`
	CapabilityID  core.ID         `json:"capability_id,omitempty"`
	OccurredAt    int64           `json:"occurred_at"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}
