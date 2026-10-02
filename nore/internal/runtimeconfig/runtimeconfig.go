// Package runtimeconfig resolves the effective runtime configuration for a
// single capability invocation.
//
// It exists because deciding what a runtimeConfig omission means belongs to
// N.O.R.E., not to the authoring SDK and not to the compiler. An author may
// declare a runtimeConfig, part of one, or none at all; this package turns
// whatever was declared into the complete, effective configuration the
// execution engine acts on.
//
// The boundary this package keeps:
//
//   - The shared schema (shared/types/core) declares what may be expressed.
//   - The compiler validates what was declared, and never substitutes a value.
//   - N.O.R.E. owns every default, here.
//
// Defaults are resolved once per execution plan rather than once per
// invocation. A registered assembly is stored exactly as it was authored, so
// changing a default never rewrites what an author deployed and never
// invalidates an existing registration: defaults live on the plan, not in the
// persisted assembly and not in its deployment hash.
package runtimeconfig

import "github.com/neuron-runtime/neuron/shared/types/core"

// Default returns the effective runtime configuration N.O.R.E. uses when an
// author declares nothing at all.
//
// Every field of the returned configuration is set from this function's own
// values, so an author never has to declare a runtimeConfig to get a
// well-defined invocation.
func Default() *core.RuntimeConfig {
	return &core.RuntimeConfig{
		Execution: &core.RuntimeExecution{
			// wait is the default: N.O.R.E. blocks for the result before
			// continuing the plan. An author opts into handing the work to a
			// separate child execution with detach.
			Mode: core.RuntimeExecutionModeWait,

			// Timeout is deliberately left empty. An empty timeout means no
			// capability-level deadline was declared, and the selected runtime
			// backend applies its own invocation bound (process and wasm both
			// own one). Naming a duration here would duplicate those constants
			// in a second place and let the two drift apart.
			Timeout: "",
		},
		Retry: &core.RuntimeRetry{
			Policy: core.RetryPolicyNone,

			// MaxAttempts counts total attempts. One means the capability is
			// invoked exactly once and never retried.
			MaxAttempts: 1,
		},
		Resources: &core.RuntimeResources{},
	}
}

// Resolve returns the effective runtime configuration for a capability, given
// whatever the author declared. A nil or empty declaration yields the
// defaults; anything the author declared wins over the default, field by field.
//
// Every field is independently overridable: an author may set one field and
// inherit the rest. A zero value means "not declared" for each field, so
// declaring an empty group changes nothing.
//
// The result is always a complete configuration owned by the caller: it never
// aliases the declared value, so resolving once per plan is safe even though
// the declared configuration is shared across every instance of an assembly.
func Resolve(declared *core.RuntimeConfig) *core.RuntimeConfig {
	effective := Default()
	if declared == nil {
		return effective
	}

	if execution := declared.Execution; execution != nil {
		if execution.Mode != "" {
			effective.Execution.Mode = execution.Mode
		}
		if execution.Timeout != "" {
			effective.Execution.Timeout = execution.Timeout
		}
	}

	if retry := declared.Retry; retry != nil {
		if retry.Policy != "" {
			effective.Retry.Policy = retry.Policy
		}
		if retry.MaxAttempts != 0 {
			effective.Retry.MaxAttempts = retry.MaxAttempts
		}
		if retry.InitialBackoff != "" {
			effective.Retry.InitialBackoff = retry.InitialBackoff
		}
		if retry.MaxBackoff != "" {
			effective.Retry.MaxBackoff = retry.MaxBackoff
		}
	}

	if declared.Resources != nil {
		effective.Resources = &core.RuntimeResources{}
	}

	return effective
}
