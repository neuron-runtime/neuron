package core

import (
	"fmt"
	"time"
)

// RuntimeConfig describes how N.O.R.E. should execute a single Capability
// through the Capability Runtime it declares. It is an instruction to the
// runtime engine, not input to the capability:
//
//	params        = the data the capability is invoked with
//	runtimeConfig = how N.O.R.E. handles that invocation
//
// A runtimeConfig belongs to the runtime declaration attached to one
// capability, never to the assembly and never to the runtime artifact. Two
// capabilities may declare the same runtime with different runtimeConfigs and
// execute with them simultaneously, because resolution and installation are
// keyed on runtime identity while the effective configuration is scoped to
// each capability invocation.
//
// Every group and every field is optional. N.O.R.E. supplies the default for
// anything an author leaves unset, so an author never has to declare a
// runtimeConfig at all. This package deliberately declares no default values:
// the schema defines what may be expressed, while the runtime engine decides
// what an omission means. Because defaults are applied after registration and
// are never written back, they do not participate in assembly identity.
type RuntimeConfig struct {
	// Execution controls how a single invocation is driven: whether the
	// runtime waits for the result or detaches, and how long it may run.
	Execution *RuntimeExecution `json:"execution,omitempty" yaml:"execution,omitempty"`

	// Retry controls whether and how a failed invocation is retried.
	Retry *RuntimeRetry `json:"retry,omitempty" yaml:"retry,omitempty"`

	// Resources declares runtime execution constraints. It is currently an
	// empty extension point: no backend enforces resource limits yet, so no
	// constraint may be declared here.
	Resources *RuntimeResources `json:"resources,omitempty" yaml:"resources,omitempty"`
}

// RuntimeExecutionMode selects whether an invocation is awaited or detached.
// It is distinct from the instance execution request mode in this package
// (ExecutionModeWait/ExecutionModeDetach), which describes how an HTTP caller
// waits for an assembly execution. This mode describes how N.O.R.E. waits for
// a single capability's result.
type RuntimeExecutionMode string

const (
	// RuntimeExecutionModeWait blocks until the capability runtime returns a
	// result, then continues the execution plan. This is the only mode the
	// engine implements.
	RuntimeExecutionModeWait RuntimeExecutionMode = "wait"

	// RuntimeExecutionModeDetach continues the execution plan without waiting
	// for the capability's result. The engine does not implement detach yet;
	// the value is accepted and carried so authors can declare intent, but it
	// currently behaves as wait.
	RuntimeExecutionModeDetach RuntimeExecutionMode = "detach"
)

// RuntimeExecution holds the invocation-level controls for one capability
// execution.
type RuntimeExecution struct {
	// Mode selects wait or detach behavior. N.O.R.E. defaults this to wait.
	Mode RuntimeExecutionMode `json:"mode,omitempty" yaml:"mode,omitempty"`

	// Timeout bounds a single invocation as a Go duration string such as
	// "5s" or "30m". An empty timeout means no capability-level deadline was
	// declared and the selected runtime backend applies its own invocation
	// bound. The default is therefore "unset" rather than a duration, so that
	// backend defaults remain the single authority for that limit instead of
	// being duplicated here.
	Timeout string `json:"timeout,omitempty" yaml:"timeout,omitempty"`
}

// RetryPolicy selects the backoff strategy applied between retry attempts.
type RetryPolicy string

const (
	// RetryPolicyNone performs a single attempt. N.O.R.E. defaults to this.
	RetryPolicyNone RetryPolicy = "none"

	// RetryPolicyFixed waits the same initial backoff before every attempt.
	RetryPolicyFixed RetryPolicy = "fixed"

	// RetryPolicyExponential doubles the backoff after every attempt, capped
	// at MaxBackoff.
	RetryPolicyExponential RetryPolicy = "exponential"
)

// RuntimeRetry controls retry behavior for one capability invocation.
//
// MaxAttempts counts total attempts, not additional retries: a value of 1
// means the capability is invoked exactly once and never retried.
type RuntimeRetry struct {
	// Policy selects the backoff strategy. N.O.R.E. defaults this to none.
	Policy RetryPolicy `json:"policy,omitempty" yaml:"policy,omitempty"`

	// MaxAttempts is the total number of invocations to attempt. Values below
	// 1 are invalid.
	MaxAttempts int `json:"maxAttempts,omitempty" yaml:"maxAttempts,omitempty"`

	// InitialBackoff is the delay before the first retry as a Go duration
	// string. It is only meaningful when Policy is not none.
	InitialBackoff string `json:"initialBackoff,omitempty" yaml:"initialBackoff,omitempty"`

	// MaxBackoff caps the exponentially growing delay as a Go duration
	// string. It is only meaningful when Policy is exponential.
	MaxBackoff string `json:"maxBackoff,omitempty" yaml:"maxBackoff,omitempty"`
}

// RuntimeResources is a reserved group for runtime execution constraints such
// as CPU, memory, or per-invocation worker ceilings.
//
// It is intentionally empty. Neither the process nor the wasm backend enforces
// resource limits, so allowing an author to declare one would promise
// enforcement the runtime does not provide. Fields are added here once a
// backend can honor them, and a backend that cannot honor a declared
// constraint must reject the assembly rather than ignore it.
type RuntimeResources struct{}

// Clone returns a deep copy of the runtimeConfig, or nil when there is
// nothing to copy. It exists so that the compiler and the planner can each own
// their own copy of a declared runtimeConfig without either sharing the
// author's pointer or restating the copy in two places.
func (c *RuntimeConfig) Clone() *RuntimeConfig {
	if c == nil {
		return nil
	}
	clone := &RuntimeConfig{}
	if c.Execution != nil {
		execution := *c.Execution
		clone.Execution = &execution
	}
	if c.Retry != nil {
		retry := *c.Retry
		clone.Retry = &retry
	}
	if c.Resources != nil {
		resources := *c.Resources
		clone.Resources = &resources
	}
	return clone
}

// IsEmpty reports whether a runtimeConfig declares nothing at all. An empty
// declaration is semantically identical to declaring no runtimeConfig, because
// N.O.R.E. resolves both to the same defaults. Callers that derive artifact
// identity from declared content use this to keep an empty group from
// registering as a meaningful change.
func (c *RuntimeConfig) IsEmpty() bool {
	if c == nil {
		return true
	}
	return c.Execution == nil && c.Retry == nil && c.Resources == nil
}

// Validate reports whether a declared runtimeConfig is well-formed. It only
// checks what an author actually declared; it never reports missing values as
// errors, because N.O.R.E. defaults every unset field.
//
// Validate belongs to the schema because it encodes which values are legal,
// not what an omission means. Applications should call it at authoring and
// compilation time so a malformed declaration fails the build rather than
// execution.
func (c *RuntimeConfig) Validate() error {
	if c == nil {
		return nil
	}
	if err := c.Execution.validate(); err != nil {
		return err
	}
	if err := c.Retry.validate(); err != nil {
		return err
	}
	return nil
}

func (e *RuntimeExecution) validate() error {
	if e == nil {
		return nil
	}
	switch e.Mode {
	case "", RuntimeExecutionModeWait, RuntimeExecutionModeDetach:
	default:
		return fmt.Errorf("runtime config: execution.mode %q is invalid (want %q or %q)",
			e.Mode, RuntimeExecutionModeWait, RuntimeExecutionModeDetach)
	}
	if _, err := ParseRuntimeDuration(e.Timeout); err != nil {
		return fmt.Errorf("runtime config: execution.timeout: %w", err)
	}
	return nil
}

func (r *RuntimeRetry) validate() error {
	if r == nil {
		return nil
	}
	switch r.Policy {
	case "", RetryPolicyNone, RetryPolicyFixed, RetryPolicyExponential:
	default:
		return fmt.Errorf("runtime config: retry.policy %q is invalid (want %q, %q or %q)",
			r.Policy, RetryPolicyNone, RetryPolicyFixed, RetryPolicyExponential)
	}
	if r.MaxAttempts < 0 {
		return fmt.Errorf("runtime config: retry.maxAttempts %d is invalid (want 1 or more)", r.MaxAttempts)
	}
	if r.MaxAttempts > 0 && (r.Policy == "" || r.Policy == RetryPolicyNone) {
		return fmt.Errorf("runtime config: retry.maxAttempts %d requires a retry policy", r.MaxAttempts)
	}
	initial, err := ParseRuntimeDuration(r.InitialBackoff)
	if err != nil {
		return fmt.Errorf("runtime config: retry.initialBackoff: %w", err)
	}
	maximum, err := ParseRuntimeDuration(r.MaxBackoff)
	if err != nil {
		return fmt.Errorf("runtime config: retry.maxBackoff: %w", err)
	}
	if initial > 0 && maximum > 0 && maximum < initial {
		return fmt.Errorf("runtime config: retry.maxBackoff (%s) is shorter than retry.initialBackoff (%s)", r.MaxBackoff, r.InitialBackoff)
	}
	return nil
}

// ParseRuntimeDuration parses a runtime config duration field. Durations are
// authored as strings such as "250ms" or "30m" rather than as time.Duration
// values, because time.Duration serializes to a bare nanosecond integer that
// no author could reasonably hand-write.
//
// An empty duration is valid and yields a zero time.Duration: it is the
// representation of "the author declared nothing", which N.O.R.E. resolves to
// its own default.
func ParseRuntimeDuration(value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%q is not a valid duration: %w", value, err)
	}
	if parsed < 0 {
		return 0, fmt.Errorf("%q must not be negative", value)
	}
	return parsed, nil
}
