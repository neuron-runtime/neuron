package engine

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

// invocation is the effective runtime policy for one capability invocation.
//
// It is derived from the runtimeConfig the planner resolved onto the plan, so it
// exists to answer the three questions the engine must not answer ad hoc on every
// invocation: whether the result is awaited at all, how long the capability may
// run, and what happens when it fails.
type invocation struct {
	mode    core.RuntimeExecutionMode
	timeout time.Duration

	// attempts is the total number of times the capability may be invoked,
	// including the first. A retry policy of none collapses to one attempt.
	attempts int

	policy         core.RetryPolicy
	initialBackoff time.Duration
	maxBackoff     time.Duration
}

// newInvocation reads the effective runtime configuration for one capability.
//
// The configuration has already been validated and defaulted by the planner, so
// this reports an error only when a plan somehow bypassed that, and it fails the
// capability loudly rather than guessing: silently ignoring a timeout it cannot
// parse would let a capability run unbounded while its author believed it was
// bounded.
func newInvocation(runtimeConfig *core.RuntimeConfig) (invocation, error) {
	inv := invocation{mode: core.RuntimeExecutionModeWait, attempts: 1, policy: core.RetryPolicyNone}
	if runtimeConfig == nil {
		return inv, nil
	}

	if declared := runtimeConfig.Execution; declared != nil {
		if declared.Mode != "" {
			inv.mode = declared.Mode
		}
		timeout, err := core.ParseRuntimeDuration(declared.Timeout)
		if err != nil {
			return invocation{}, fmt.Errorf("execution.timeout: %w", err)
		}
		inv.timeout = timeout
	}

	if declared := runtimeConfig.Retry; declared != nil {
		if declared.Policy != "" {
			inv.policy = declared.Policy
		}
		if declared.MaxAttempts != 0 {
			inv.attempts = declared.MaxAttempts
		}
		var err error
		if inv.initialBackoff, err = core.ParseRuntimeDuration(declared.InitialBackoff); err != nil {
			return invocation{}, fmt.Errorf("retry.initialBackoff: %w", err)
		}
		if inv.maxBackoff, err = core.ParseRuntimeDuration(declared.MaxBackoff); err != nil {
			return invocation{}, fmt.Errorf("retry.maxBackoff: %w", err)
		}
	}

	if inv.attempts < 1 {
		inv.attempts = 1
	}
	if inv.policy == core.RetryPolicyNone {
		inv.attempts = 1
	}
	return inv, nil
}

// Detached reports whether this invocation hands its work off instead of running
// it in place.
func (inv invocation) Detached() bool {
	return inv.mode == core.RuntimeExecutionModeDetach
}

// retryNotice reports a scheduled re-attempt so the engine can publish it.
type retryNotice struct {
	attempt     int
	nextAttempt int
	delay       time.Duration
	err         error
}

// run drives one capability invocation under this policy.
//
// timeout is the caller's context for the whole attempt sequence. Every attempt
// and every backoff sleep is bounded by a single deadline derived from it, so an
// execution timeout limits the capability's total cost rather than each attempt
// individually. Without that, an exponential backoff could keep a capability
// alive far past the timeout its author declared.
//
// execute is called once per attempt. onRetry, when set, is called before each
// backoff sleep.
func (inv invocation) run(ctx context.Context, capabilityID core.ID, execute func(context.Context) (map[string]any, error), onRetry func(retryNotice)) (map[string]any, error) {
	attemptCtx, cancel := inv.attemptContext(ctx)
	defer cancel()

	var lastErr error
	for attempt := 1; attempt <= inv.attempts; attempt++ {
		if attemptCtx.Err() != nil {
			return nil, inv.deadlineError(ctx, attemptCtx, capabilityID, lastErr)
		}

		output, err := execute(attemptCtx)
		if err == nil {
			return output, nil
		}
		lastErr = err

		if !inv.shouldRetry(ctx, attemptCtx, attempt) {
			// A deadline can fire while the capability runtime is running
			// rather than while N.O.R.E. sleeps, and a backend reports that by
			// returning whatever its own context produced. Translate it here so
			// a timeout reads the same whether it interrupted the attempt or
			// the backoff after it.
			if attemptCtx.Err() != nil {
				return nil, inv.deadlineError(ctx, attemptCtx, capabilityID, lastErr)
			}
			return nil, err
		}

		delay := inv.backoff(attempt)
		if onRetry != nil {
			onRetry(retryNotice{attempt: attempt, nextAttempt: attempt + 1, delay: delay, err: err})
		}
		if !sleep(attemptCtx, delay) {
			return nil, inv.deadlineError(ctx, attemptCtx, capabilityID, lastErr)
		}
	}
	return nil, lastErr
}

// shouldRetry reports whether another attempt may follow a failure.
//
// Neuron has no capability-level error taxonomy, so it does not guess which
// failures are transient — inventing one would silently make some failures
// retried and others not, for reasons no author could see. It retries every
// failure except the two where retrying is guaranteed to be wrong: the caller is
// shutting down, or the invocation has already run out of time.
func (inv invocation) shouldRetry(callerCtx, attemptCtx context.Context, attempt int) bool {
	if attempt >= inv.attempts {
		return false
	}
	if callerCtx.Err() != nil {
		return false
	}
	return attemptCtx.Err() == nil
}

// backoff returns the delay to wait after a failed attempt.
func (inv invocation) backoff(attempt int) time.Duration {
	switch inv.policy {
	case core.RetryPolicyFixed:
		return inv.initialBackoff
	case core.RetryPolicyExponential:
		return exponentialBackoff(inv.initialBackoff, inv.maxBackoff, attempt)
	default:
		return 0
	}
}

// attemptContext bounds one capability invocation. An author who declared no
// timeout declares no capability-level deadline, and the selected capability
// runtime backend remains the authority for its own invocation bound; imposing a
// duration here would duplicate those constants and let the two drift apart. The
// context is still derived so cancellation propagates either way.
func (inv invocation) attemptContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if inv.timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, inv.timeout)
}

// deadlineError explains why an invocation stopped without succeeding. It
// distinguishes a deadline N.O.R.E. imposed from a caller that went away, because
// the two mean different things to whoever reads the failure.
func (inv invocation) deadlineError(callerCtx, attemptCtx context.Context, capabilityID core.ID, lastErr error) error {
	if attemptCtx.Err() == context.DeadlineExceeded {
		if inv.timeout > 0 {
			return fmt.Errorf("capability %s timed out after %s", capabilityID, inv.timeout)
		}
		return fmt.Errorf("capability %s exceeded its execution deadline", capabilityID)
	}
	if callerCtx.Err() != nil {
		return fmt.Errorf("capability %s was cancelled before it completed", capabilityID)
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("capability %s exceeded its execution deadline", capabilityID)
}

// exponentialBackoff doubles the delay after each failed attempt, saturating at
// maxBackoff instead of overflowing. A zero maximum means uncapped, so growth
// stops at the largest delay a context can represent.
func exponentialBackoff(initial, maxBackoff time.Duration, attempt int) time.Duration {
	if initial <= 0 {
		return 0
	}
	delay := initial
	for step := 1; step < attempt; step++ {
		if maxBackoff > 0 && delay >= maxBackoff {
			return maxBackoff
		}
		if delay > math.MaxInt64/2 {
			if maxBackoff > 0 {
				return maxBackoff
			}
			return math.MaxInt64
		}
		delay *= 2
	}
	if maxBackoff > 0 && delay > maxBackoff {
		return maxBackoff
	}
	return delay
}

// sleep waits out a backoff delay, reporting false when the invocation ran out of
// time first. Returning rather than sleeping to completion is what keeps a
// backoff from extending the execution past its declared timeout.
func sleep(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
