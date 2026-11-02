package contracts

import (
	"context"

	core2 "github.com/neuron-runtime/neuron/shared/types/core"
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

// NopLogger discards every log message.
//
// It is the logger for the execution paths that have no event bus to publish
// onto — a direct invocation, for instance, which runs a capability runtime
// outside the engine. Handing those paths a nil logger instead would turn
// every diagnostic a capability runtime writes into a panic, so the seam is
// always satisfied.
type NopLogger struct{}

func (NopLogger) Debug(context.Context, string, ...Field) {}
func (NopLogger) Info(context.Context, string, ...Field)  {}
func (NopLogger) Warn(context.Context, string, ...Field)  {}
func (NopLogger) Error(context.Context, string, ...Field) {}

// LoggerOrNop returns l, or a logger that discards everything when l is nil.
//
// The logger is a seam that some execution paths do not provide — a direct
// invocation has no event bus to publish onto — so a capability runtime that
// logs must not have to know which path it runs under.
func LoggerOrNop(l Logger) Logger {
	if l == nil {
		return NopLogger{}
	}
	return l
}

type ExecutionContext struct {
	ExecutionID   core2.ID
	CorrelationID core2.ID

	Capability core2.Capability
	Params     map[string]any

	// CapabilityConfigurations contains the fully resolved configuration for
	// this single Capability execution. It contains no {{ ... }} placeholders.
	//
	// This is user-authored input to the capability and is deliberately
	// distinct from RuntimeConfig.
	CapabilityConfigurations map[string]any

	// RuntimeConfig is the effective runtime configuration for this single
	// capability invocation: how N.O.R.E. should drive the capability through
	// its declared Capability Runtime. Defaults are already resolved, so every
	// field is populated.
	//
	// It belongs to the runtime engine, not to the capability. Implementations
	// must never merge it into Params or CapabilityConfigurations, and it is
	// never sent to the capability runtime as capability input.
	RuntimeConfig *core2.RuntimeConfig

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
