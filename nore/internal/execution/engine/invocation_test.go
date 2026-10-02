package engine

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

func TestNewInvocationDefaultsToASingleAwaitedAttempt(t *testing.T) {
	inv, err := newInvocation(nil)
	if err != nil {
		t.Fatalf("newInvocation: %v", err)
	}
	if inv.Detached() {
		t.Fatal("a missing runtime configuration must default to wait")
	}
	if inv.attempts != 1 || inv.policy != core.RetryPolicyNone {
		t.Fatalf("attempts = %d, policy = %q, want one attempt and no retry", inv.attempts, inv.policy)
	}
	if inv.timeout != 0 {
		t.Fatalf("timeout = %s, want unset", inv.timeout)
	}
}

func TestNewInvocationCollapsesNonePolicyToOneAttempt(t *testing.T) {
	inv, err := newInvocation(&core.RuntimeConfig{
		Retry: &core.RuntimeRetry{Policy: core.RetryPolicyNone, MaxAttempts: 9},
	})
	if err != nil {
		t.Fatalf("newInvocation: %v", err)
	}
	if inv.attempts != 1 {
		t.Fatalf("a none policy must collapse to one attempt, got %d", inv.attempts)
	}
}

func TestNewInvocationRejectsUnparsableTimeout(t *testing.T) {
	if _, err := newInvocation(&core.RuntimeConfig{
		Execution: &core.RuntimeExecution{Timeout: "soon"},
	}); err == nil {
		t.Fatal("an unparsable timeout must be an error, not a silent default")
	}
}

func TestInvocationRetriesUntilSuccess(t *testing.T) {
	inv, err := newInvocation(&core.RuntimeConfig{
		Retry: &core.RuntimeRetry{Policy: core.RetryPolicyFixed, MaxAttempts: 3, InitialBackoff: "1ms"},
	})
	if err != nil {
		t.Fatalf("newInvocation: %v", err)
	}

	var calls int32
	var retries []retryNotice
	output, err := inv.run(context.Background(), "step", func(context.Context) (map[string]any, error) {
		if atomic.AddInt32(&calls, 1) < 3 {
			return nil, errors.New("transient")
		}
		return map[string]any{"ok": true}, nil
	}, func(notice retryNotice) { retries = append(retries, notice) })
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if calls != 3 {
		t.Fatalf("execute was called %d times, want 3", calls)
	}
	if output["ok"] != true {
		t.Fatalf("output = %+v, want the third attempt's result", output)
	}
	if len(retries) != 2 {
		t.Fatalf("reported %d retries, want 2", len(retries))
	}
	if retries[0].attempt != 1 || retries[0].nextAttempt != 2 {
		t.Fatalf("first retry = %+v, want attempt 1 -> 2", retries[0])
	}
}

func TestInvocationDoesNotRetryAfterExhaustingAttempts(t *testing.T) {
	inv, err := newInvocation(&core.RuntimeConfig{
		Retry: &core.RuntimeRetry{Policy: core.RetryPolicyFixed, MaxAttempts: 2, InitialBackoff: "1ms"},
	})
	if err != nil {
		t.Fatalf("newInvocation: %v", err)
	}
	var calls int32
	_, err = inv.run(context.Background(), "step", func(context.Context) (map[string]any, error) {
		atomic.AddInt32(&calls, 1)
		return nil, errors.New("always failing")
	}, nil)
	if err == nil {
		t.Fatal("run must return the final failure")
	}
	if calls != 2 {
		t.Fatalf("execute was called %d times, want exactly maxAttempts", calls)
	}
}

func TestInvocationTimeoutBoundsBackoff(t *testing.T) {
	// A backoff longer than the remaining budget must not extend the
	// invocation past its deadline: the timeout bounds the whole sequence, not
	// each attempt.
	inv, err := newInvocation(&core.RuntimeConfig{
		Execution: &core.RuntimeExecution{Timeout: "50ms"},
		Retry:     &core.RuntimeRetry{Policy: core.RetryPolicyFixed, MaxAttempts: 5, InitialBackoff: "1s"},
	})
	if err != nil {
		t.Fatalf("newInvocation: %v", err)
	}
	var calls int32
	started := time.Now()
	_, err = inv.run(context.Background(), "step", func(context.Context) (map[string]any, error) {
		atomic.AddInt32(&calls, 1)
		return nil, errors.New("failed")
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if calls != 1 {
		t.Fatalf("execute was called %d times; the backoff should have consumed the remaining budget", calls)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("run took %s; the timeout must bound the sleep", elapsed)
	}
}

func TestInvocationReportsTimeoutThatInterruptsTheAttempt(t *testing.T) {
	// A backend that observes its own context ending reports the deadline as a
	// plain error rather than through N.O.R.E.'s sleep. The engine must still
	// surface it as an invocation timeout, not as the backend's raw context
	// error, so an author sees the same message either way.
	inv, err := newInvocation(&core.RuntimeConfig{
		Execution: &core.RuntimeExecution{Timeout: "20ms"},
	})
	if err != nil {
		t.Fatalf("newInvocation: %v", err)
	}
	_, err = inv.run(context.Background(), "step", func(ctx context.Context) (map[string]any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout", err)
	}
}

func TestInvocationDoesNotRetryWhenCallerCancelled(t *testing.T) {
	inv, err := newInvocation(&core.RuntimeConfig{
		Retry: &core.RuntimeRetry{Policy: core.RetryPolicyFixed, MaxAttempts: 5, InitialBackoff: "1ms"},
	})
	if err != nil {
		t.Fatalf("newInvocation: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls int32
	_, err = inv.run(ctx, "step", func(context.Context) (map[string]any, error) {
		atomic.AddInt32(&calls, 1)
		return nil, errors.New("failed")
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("err = %v, want a cancellation", err)
	}
	if calls != 0 {
		t.Fatalf("execute was called %d times after cancellation, want 0", calls)
	}
}

func TestExponentialBackoffSaturates(t *testing.T) {
	initial := 100 * time.Millisecond
	maximum := time.Second
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 1, want: 100 * time.Millisecond},
		{attempt: 2, want: 200 * time.Millisecond},
		{attempt: 3, want: 400 * time.Millisecond},
		{attempt: 5, want: time.Second},
		{attempt: 40, want: time.Second},
	}
	for _, testCase := range cases {
		if got := exponentialBackoff(initial, maximum, testCase.attempt); got != testCase.want {
			t.Errorf("exponentialBackoff(attempt=%d) = %s, want %s", testCase.attempt, got, testCase.want)
		}
	}
}

func TestExponentialBackoffUncappedSaturatesAtMaxInt64(t *testing.T) {
	// Without a ceiling the doubling must stop at the largest representable
	// delay instead of overflowing into a negative duration.
	if got := exponentialBackoff(time.Second, 0, 200); got <= 0 {
		t.Fatalf("uncapped backoff overflowed to %s", got)
	}
}
