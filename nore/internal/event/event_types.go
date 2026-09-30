package event

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
	default:
		return "unknown"
	}
}
