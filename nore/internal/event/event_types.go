package event

// Type identifies an execution lifecycle event.
//
// The numeric values are part of the persisted event format: the event store
// writes them as-is, so a type may only ever be appended. Reordering or removing
// one would silently reinterpret events already on disk.
type Type uint16

const (
	Unknown Type = iota
	ExecutionStarted
	ExecutionCompleted
	ExecutionFailed
	ExecutionCancelled
	CapabilityReady
	CapabilityStarted
	CapabilityCompleted
	CapabilityFailed
	CapabilityLog
	// CapabilityDetached records that a capability's work was handed off to a
	// separate execution. It ends this capability's participation in the
	// execution but is not an execution-terminal event: the work itself
	// continues under the task created for it.
	CapabilityDetached
	// CapabilityRetry records a scheduled re-attempt of one capability
	// invocation. Retries are internal to a single capability execution, so
	// this is progress rather than a new execution.
	CapabilityRetry
	// CapabilityCancelled records that a capability stopped without reaching an
	// outcome of its own, because something outside it ended first: the
	// execution was cancelled, or a sibling capability failed.
	//
	// It exists so that distinction survives to consumers. Without it a
	// capability stopped as a consequence of another failure was published as a
	// failure of its own, so a correct implementation was reported as broken and
	// a client could not tell the two apart without reading the message text.
	// This is not an execution-terminal event; the execution's own terminal event
	// already records why everything stopped.
	CapabilityCancelled
)

const All Type = 0xffff

// IsTerminal reports whether the type ends an execution. A terminal event is
// the last event an execution produces, so consumers that only wait for the
// outcome of an execution can stop once one has been delivered.
func (t Type) IsTerminal() bool {
	switch t {
	case ExecutionCompleted, ExecutionFailed, ExecutionCancelled:
		return true
	default:
		return false
	}
}

func (t Type) String() string {
	switch t {
	case ExecutionStarted:
		return "execution.started"
	case ExecutionCompleted:
		return "execution.completed"
	case ExecutionFailed:
		return "execution.failed"
	case ExecutionCancelled:
		return "execution.cancelled"
	case CapabilityReady:
		return "capability.ready"
	case CapabilityStarted:
		return "capability.started"
	case CapabilityCompleted:
		return "capability.completed"
	case CapabilityFailed:
		return "capability.failed"
	case CapabilityLog:
		return "capability.log"
	case CapabilityDetached:
		return "capability.detached"
	case CapabilityRetry:
		return "capability.retry"
	case CapabilityCancelled:
		return "capability.cancelled"
	default:
		return "unknown"
	}
}
