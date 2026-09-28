package contracts

import (
	"context"

	core2 "github.com/Muhammad-Jay/neuron/shared/types/core"
)

// Field is a single structured key/value pair attached to a log message.
type Field struct {
	Key   string
	Value any
}

func F(key string, value any) Field {
	return Field{Key: key, Value: value}
}

// Logger is the logging seam handed to capability runtimes. Implementations
// usually publish CapabilityLog events onto the execution's event bus so logs
// flow through the same stream as lifecycle events instead of raw stdout
// writes. Logging is always best-effort and must never fail an execution.
type Logger interface {
	Debug(ctx context.Context, message string, fields ...Field)
	Info(ctx context.Context, message string, fields ...Field)
	Warn(ctx context.Context, message string, fields ...Field)
	Error(ctx context.Context, message string, fields ...Field)
}

type ExecutionContext struct {
	ExecutionID   core2.ID
	CorrelationID core2.ID

	Capability core2.Capability
	Params     map[string]any

	// CapabilityConfigurations contains the fully resolved configuration for
	// this single Capability execution. It contains no {{ ... }} placeholders.
	CapabilityConfigurations map[string]any

	// Logger is bound to this capability execution. Use it for any diagnostic
	// output instead of writing to stdout.
	Logger Logger
}

type CapabilityRuntime interface {
	Execute(ctx context.Context, execution ExecutionContext) (map[string]any, error)
}

// CapabilityRuntimeCloser is implemented by runtimes that own runtime
// resources (for example a wasm backend) that must be released when the
// owning instance stops. Instances call Close on every registered runtime
// before shutting down.
type CapabilityRuntimeCloser interface {
	Close() error
}

type CapabilityRuntimeRegistry interface {
	Register(runtimeType core2.CapabilityRuntimeType, runtime CapabilityRuntime) error
	Resolve(runtimeType core2.CapabilityRuntimeType) (CapabilityRuntime, error)
}