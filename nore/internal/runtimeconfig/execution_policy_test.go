package runtimeconfig

import (
	"strings"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

func TestDefaultExecutionPolicy(t *testing.T) {
	got := DefaultExecutionPolicy()

	if got.WaitDeadlinePolicy != core.WaitDeadlinePolicyFail {
		t.Errorf("wait deadline policy = %q, want %q", got.WaitDeadlinePolicy, core.WaitDeadlinePolicyFail)
	}
	if got.MaxLifetime != 30*24*time.Hour {
		t.Errorf("max lifetime = %s, want 30 days", got.MaxLifetime)
	}
	if got.TimerPollInterval != time.Second {
		t.Errorf("timer poll interval = %s, want 1s", got.TimerPollInterval)
	}
	if got.TimerPollBatchSize != 256 {
		t.Errorf("timer poll batch size = %d, want 256", got.TimerPollBatchSize)
	}
}

func TestDefaultExecutionPolicyDoesNotEnableDurability(t *testing.T) {
	// Timer polling and deadline policy are inert without a durable wait store.
	// Their defaults must therefore leave non-durable executions untouched: the
	// resolved policy carries no durability switch and no wait store of its own.
	got := DefaultExecutionPolicy()
	if got.WaitDeadlinePolicy == "" || got.MaxLifetime == 0 || got.TimerPollInterval == 0 {
		t.Fatalf("defaults must be fully populated: %+v", got)
	}
	if got.MaxLifetime != core.DefaultExecutionMaxLifetime {
		t.Fatalf("max lifetime default %s diverged from the shared contract", got.MaxLifetime)
	}
}

func TestResolveExecutionPolicyAcceptsAllDeadlinePolicies(t *testing.T) {
	for _, want := range []string{
		core.WaitDeadlinePolicyFail,
		core.WaitDeadlinePolicyCancel,
		core.WaitDeadlinePolicyContinue,
	} {
		t.Run(want, func(t *testing.T) {
			got, err := ResolveExecutionPolicy(ExecutionPolicyInput{WaitDeadlinePolicy: want})
			if err != nil {
				t.Fatalf("resolve policy: %v", err)
			}
			if got.WaitDeadlinePolicy != want {
				t.Errorf("policy = %q, want %q", got.WaitDeadlinePolicy, want)
			}
		})
	}
}

func TestResolveExecutionPolicyZeroMaxLifetimeDisablesBound(t *testing.T) {
	got, err := ResolveExecutionPolicy(ExecutionPolicyInput{MaxLifetime: "0"})
	if err != nil {
		t.Fatalf("resolve policy: %v", err)
	}
	if got.MaxLifetime != 0 {
		t.Errorf("max lifetime = %s, want 0 (disabled)", got.MaxLifetime)
	}
	if got.WaitDeadlinePolicy != core.DefaultWaitDeadlinePolicy {
		t.Errorf("unrelated field changed: policy = %q", got.WaitDeadlinePolicy)
	}
}

func TestResolveExecutionPolicyAppliesBoundedTimerValues(t *testing.T) {
	got, err := ResolveExecutionPolicy(ExecutionPolicyInput{
		TimerPollInterval:  "5s",
		TimerPollBatchSize: 64,
	})
	if err != nil {
		t.Fatalf("resolve policy: %v", err)
	}
	if got.TimerPollInterval != 5*time.Second {
		t.Errorf("interval = %s, want 5s", got.TimerPollInterval)
	}
	if got.TimerPollBatchSize != 64 {
		t.Errorf("batch size = %d, want 64", got.TimerPollBatchSize)
	}
}

func TestResolveExecutionPolicyRejectsInvalidValues(t *testing.T) {
	cases := map[string]struct {
		input ExecutionPolicyInput
		want  []string
	}{
		"unknown deadline policy": {
			input: ExecutionPolicyInput{WaitDeadlinePolicy: "later"},
			want:  []string{"wait deadline policy", "later", "fail", "cancel", "continue"},
		},
		"unparsable max lifetime": {
			input: ExecutionPolicyInput{MaxLifetime: "forever"},
			want:  []string{"execution max lifetime", "forever"},
		},
		"negative max lifetime": {
			input: ExecutionPolicyInput{MaxLifetime: "-1h"},
			want:  []string{"execution max lifetime", "negative"},
		},
		"unparsable poll interval": {
			input: ExecutionPolicyInput{TimerPollInterval: "often"},
			want:  []string{"timer poll interval", "often"},
		},
		"zero poll interval": {
			input: ExecutionPolicyInput{TimerPollInterval: "0"},
			want:  []string{"timer poll interval", "greater than 0"},
		},
		"excessive poll interval": {
			input: ExecutionPolicyInput{TimerPollInterval: "2m"},
			want:  []string{"timer poll interval", "must not exceed"},
		},
		"negative batch size": {
			input: ExecutionPolicyInput{TimerPollBatchSize: -1},
			want:  []string{"timer poll batch size", "-1"},
		},
		"excessive batch size": {
			input: ExecutionPolicyInput{TimerPollBatchSize: core.MaxTimerPollBatchSize + 1},
			want:  []string{"timer poll batch size", "4097"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ResolveExecutionPolicy(tc.input)
			if err == nil {
				t.Fatalf("expected %+v to be rejected", tc.input)
			}
			for _, fragment := range tc.want {
				if !strings.Contains(err.Error(), fragment) {
					t.Errorf("error %q does not mention %q", err, fragment)
				}
			}
		})
	}
}

func TestResolveExecutionPolicyIsIndependentPerCall(t *testing.T) {
	first, err := ResolveExecutionPolicy(ExecutionPolicyInput{TimerPollBatchSize: 8})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ResolveExecutionPolicy(ExecutionPolicyInput{WaitDeadlinePolicy: core.WaitDeadlinePolicyContinue})
	if err != nil {
		t.Fatal(err)
	}

	if first.TimerPollBatchSize != 8 || second.TimerPollBatchSize != core.DefaultTimerPollBatchSize {
		t.Fatalf("batch sizes leaked between resolutions: %d, %d", first.TimerPollBatchSize, second.TimerPollBatchSize)
	}
	if first.WaitDeadlinePolicy != core.DefaultWaitDeadlinePolicy || second.WaitDeadlinePolicy != core.WaitDeadlinePolicyContinue {
		t.Fatalf("policies leaked between resolutions: %q, %q", first.WaitDeadlinePolicy, second.WaitDeadlinePolicy)
	}
}
