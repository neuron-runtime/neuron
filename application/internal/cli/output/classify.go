// Event classification: the renderer separates the stream into live updates,
// permanent static lines, and the terminal event that ends an execution. The
// renderer never infers completion from log content — only a terminal event
// ends a run.
package output

import "github.com/Muhammad-Jay/neuron/shared/types/protocol"

// Kind classifies how an event should be presented.
type Kind int

const (
	// KindLive updates the live region (capability lifecycle while running).
	KindLive Kind = iota
	// KindStatic renders a permanent line (a capability reached a state).
	KindStatic
	// KindTerminal ends the execution presentation.
	KindTerminal
)

// Classify maps an event type to a presentation kind.
func Classify(evt protocol.StreamEvent) Kind {
	switch evt.Type {
	case "execution.started":
		return KindLive
	case "capability.ready", "capability.started", "capability.log":
		return KindLive
	case "capability.completed", "capability.failed":
		return KindStatic
	case "execution.completed", "execution.failed", "execution.cancelled":
		return KindTerminal
	default:
		return KindStatic
	}
}

// IsTerminalEvent reports whether the event marks a terminal execution state.
func IsTerminalEvent(evt protocol.StreamEvent) bool {
	return Classify(evt) == KindTerminal
}
