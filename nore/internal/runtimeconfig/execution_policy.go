package runtimeconfig

import (
	"time"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

// ExecutionPolicy is the effective, deployment-level policy N.O.R.E. applies to
// waits and their due timers. It is deliberately separate from the per-capability
// RuntimeConfig: an assembly author never declares it, it is not part of
// assembly identity, and applying it never turns a non-durable execution into a
// durable one.
type ExecutionPolicy struct {
	// WaitDeadlinePolicy is the action applied when a wait deadline passes.
	WaitDeadlinePolicy string
	// MaxLifetime bounds an execution's total lifetime; zero disables it. It is
	// distinct from a capability's invocation timeout.
	MaxLifetime time.Duration
	// TimerPollInterval is the cadence of the due-timer consumer.
	TimerPollInterval time.Duration
	// TimerPollBatchSize bounds the timers claimed in one poll.
	TimerPollBatchSize int
}

// ExecutionPolicyInput is the unresolved form of ExecutionPolicy, matching the
// strings and integers an operator writes in deployment configuration or passes
// on the command line. Zero values mean "not declared".
type ExecutionPolicyInput struct {
	WaitDeadlinePolicy string
	MaxLifetime        string
	TimerPollInterval  string
	TimerPollBatchSize int
}

// ResolveExecutionPolicy fills every unset field from N.O.R.E.'s defaults and
// validates what was declared. An error leaves the policy unusable rather than
// returning a partially applied one, so a malformed deployment value cannot be
// silently replaced with a bound the operator did not ask for.
//
// The result is a value, not a pointer to shared state: T-050 can pass it to the
// host's wait lifecycle and timer poller without either mutating the other's
// configuration.
func ResolveExecutionPolicy(input ExecutionPolicyInput) (ExecutionPolicy, error) {
	return DefaultExecutionPolicy().applyInput(input)
}

// applyInput applies an input over already-defaulted values. Each field is
// resolved independently so a deployment may override one bound and inherit the
// rest.
func (policy ExecutionPolicy) applyInput(input ExecutionPolicyInput) (ExecutionPolicy, error) {
	if input.WaitDeadlinePolicy != "" {
		normalized, err := core.NormalizeWaitDeadlinePolicy(input.WaitDeadlinePolicy)
		if err != nil {
			return ExecutionPolicy{}, err
		}
		policy.WaitDeadlinePolicy = normalized
	}
	if input.MaxLifetime != "" {
		parsed, err := core.ValidateExecutionMaxLifetime(input.MaxLifetime)
		if err != nil {
			return ExecutionPolicy{}, err
		}
		policy.MaxLifetime = parsed
	}
	if input.TimerPollInterval != "" {
		parsed, err := core.ValidateTimerPollInterval(input.TimerPollInterval)
		if err != nil {
			return ExecutionPolicy{}, err
		}
		policy.TimerPollInterval = parsed
	}
	if input.TimerPollBatchSize != 0 {
		if err := core.ValidateTimerPollBatchSize(input.TimerPollBatchSize); err != nil {
			return ExecutionPolicy{}, err
		}
		policy.TimerPollBatchSize = input.TimerPollBatchSize
	}
	return policy, nil
}

// DefaultExecutionPolicy returns the policy N.O.R.E. applies when a deployment
// declares nothing. It resolves to the fail deadline policy, a 30-day maximum
// lifetime, a 1s poll interval, and a batch of 256. None of these defaults
// enables durable waits on its own: without a wait store the host keeps its
// non-durable, event-driven behavior.
func DefaultExecutionPolicy() ExecutionPolicy {
	return ExecutionPolicy{
		WaitDeadlinePolicy: core.DefaultWaitDeadlinePolicy,
		MaxLifetime:        core.DefaultExecutionMaxLifetime,
		TimerPollInterval:  core.DefaultTimerPollInterval,
		TimerPollBatchSize: core.DefaultTimerPollBatchSize,
	}
}
