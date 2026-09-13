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

// Logger is the logging seam handed to executors. Implementations usually
// publish ServiceLog events onto the execution's event bus so logs flow
// through the same stream as lifecycle events instead of raw stdout writes.
// Logging is always best-effort and must never fail an execution.
type Logger interface {
	Debug(ctx context.Context, message string, fields ...Field)
	Info(ctx context.Context, message string, fields ...Field)
	Warn(ctx context.Context, message string, fields ...Field)
	Error(ctx context.Context, message string, fields ...Field)
}

type ExecutionContext struct {
	ExecutionID   core2.ID
	CorrelationID core2.ID

	Service core2.Service
	Input   map[string]any

	// ServiceConfigurations contains the fully resolved configuration for this
	// single Service execution. It contains no {{ ... }} placeholders.
	ServiceConfigurations map[string]any

	// Logger is bound to this service execution. Use it for any diagnostic
	// output instead of writing to stdout.
	Logger Logger
}

type Executor interface {
	Execute(ctx context.Context, execution ExecutionContext) (map[string]any, error)
}

// ExecutorCloser is implemented by executors that own runtime resources (for
// example a wasm runtime) that must be released when the owning instance
// stops. Instances call Close on every registered executor before shutting
// down.
type ExecutorCloser interface {
	Close() error
}

type ExecutorRegistry interface {
	Register(executorType core2.ExecutorType, executor Executor) error
	Resolve(executorType core2.ExecutorType) (Executor, error)
}
