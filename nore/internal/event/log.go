package event

import "github.com/neuron-runtime/neuron/shared/types/core"

// LogLevel is the severity of a CapabilityLog payload.
type LogLevel string

const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

// LogPayload is carried by CapabilityLog events and represents one structured
// diagnostic line emitted by an executing capability. It is what capability runtimes should
// use instead of writing directly to stdout.
type LogPayload struct {
	Level   LogLevel
	Message string
	NodeID  core.ID
	Fields  []Field
}

// Field is a single structured key/value pair on a log line.
type Field struct {
	Key   string
	Value any
}
