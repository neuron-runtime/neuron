package protocol

import (
	"encoding/json"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

// WaitItem is the wire view of one capability parked in a wait. It exposes the
// wait reason, which signal or timer is expected, the correlation key used to
// match a delivery, and the wait deadline. It is read-only: the API never
// mutates wait state through this shape.
type WaitItem struct {
	CapabilityID   core.ID `json:"capability_id"`
	WaitType       string  `json:"wait_type"`
	ExpectedSignal string  `json:"expected_signal,omitempty"`
	WaitReason     string  `json:"wait_reason,omitempty"`
	CorrelationKey string  `json:"correlation_key,omitempty"`
	// Deadline is the wait's WaitUntil in Unix nanoseconds. Nil means the wait
	// has no deadline and stays open until a signal, cancellation, or the
	// execution's maximum lifetime.
	Deadline *int64 `json:"deadline,omitempty"`
}

type ExecutionItem struct {
	ID            core.ID `json:"id"`
	CorrelationID core.ID `json:"correlation_id"`
	Status        string  `json:"status"`
	StartedAt     *int64  `json:"started_at,omitempty"`
	CompletedAt   *int64  `json:"completed_at,omitempty"`
	Error         string  `json:"error,omitempty"`

	// ParentExecutionID is set when this execution took over detached work from
	// another execution in the same instance. It lets a caller walk from a
	// detached task back to the execution whose caller handed the work off.
	ParentExecutionID core.ID `json:"parent_execution_id,omitempty"`

	// Waits lists the capabilities currently parked in a wait. It is populated
	// for suspended executions so a caller can see why they are waiting and
	// what would resume them.
	Waits []WaitItem `json:"waits,omitempty"`
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
