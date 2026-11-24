package config

import (
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

func TestOperationalPolicyDefaults(t *testing.T) {
	cfg, err := Load(Options{GlobalPath: "/nonexistent/global.yaml", ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Runtime.Execution.WaitDeadlinePolicy != core.WaitDeadlinePolicyFail {
		t.Errorf("wait deadline policy = %q, want %q", cfg.Runtime.Execution.WaitDeadlinePolicy, core.WaitDeadlinePolicyFail)
	}
	if cfg.Runtime.Execution.MaxLifetime != core.DefaultExecutionMaxLifetime.String() {
		t.Errorf("max lifetime = %q, want %q", cfg.Runtime.Execution.MaxLifetime, core.DefaultExecutionMaxLifetime.String())
	}
	if cfg.Runtime.Execution.TimerPoll.Interval != core.DefaultTimerPollInterval.String() {
		t.Errorf("timer poll interval = %q, want %q", cfg.Runtime.Execution.TimerPoll.Interval, core.DefaultTimerPollInterval.String())
	}
	if cfg.Runtime.Execution.TimerPoll.BatchSize != core.DefaultTimerPollBatchSize {
		t.Errorf("timer poll batch size = %d, want %d", cfg.Runtime.Execution.TimerPoll.BatchSize, core.DefaultTimerPollBatchSize)
	}
	// Defaults must not turn a non-durable deployment into a durable one.
	if cfg.Runtime.Execution.Durability != DurabilityHistory {
		t.Errorf("default durability = %q, want %q", cfg.Runtime.Execution.Durability, DurabilityHistory)
	}
}

func TestMaxLifetimeIsDistinctFromCapabilityTimeout(t *testing.T) {
	cfg, err := Load(Options{
		GlobalPath: "/nonexistent/global.yaml",
		ProjectDir: t.TempDir(),
		CLI: map[string]any{
			"runtime.execution.maxLifetime": "48h",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.Execution.MaxLifetime != "48h" {
		t.Errorf("max lifetime = %q, want 48h", cfg.Runtime.Execution.MaxLifetime)
	}
	if cfg.Runtime.Execution.Timeout != "30m" {
		t.Errorf("capability timeout changed to %q, want 30m", cfg.Runtime.Execution.Timeout)
	}
}

func TestGlobalConfigAcceptsOperationalPolicy(t *testing.T) {
	cfg, err := loadGlobalConfig(t, `
runtime:
  execution:
    waitDeadlinePolicy: continue
    maxLifetime: 0
    timerPoll:
      interval: 5s
      batchSize: 64
`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.Execution.WaitDeadlinePolicy != core.WaitDeadlinePolicyContinue {
		t.Errorf("policy = %q, want continue", cfg.Runtime.Execution.WaitDeadlinePolicy)
	}
	if cfg.Runtime.Execution.MaxLifetime != "0" {
		t.Errorf("max lifetime = %q, want 0", cfg.Runtime.Execution.MaxLifetime)
	}
	if cfg.Runtime.Execution.TimerPoll.Interval != "5s" {
		t.Errorf("interval = %q, want 5s", cfg.Runtime.Execution.TimerPoll.Interval)
	}
	if cfg.Runtime.Execution.TimerPoll.BatchSize != 64 {
		t.Errorf("batch size = %d, want 64", cfg.Runtime.Execution.TimerPoll.BatchSize)
	}
}

func TestOperationalPolicyEnvironmentOverrides(t *testing.T) {
	t.Setenv("NEURON_WAIT_DEADLINE_POLICY", core.WaitDeadlinePolicyCancel)
	t.Setenv("NEURON_EXECUTION_MAX_LIFETIME", "24h")
	t.Setenv("NEURON_TIMER_POLL_INTERVAL", "2s")
	t.Setenv("NEURON_TIMER_POLL_BATCH_SIZE", "32")

	cfg, err := Load(Options{
		GlobalPath:  "/nonexistent/global.yaml",
		ProjectDir:  t.TempDir(),
		Environment: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.Execution.WaitDeadlinePolicy != core.WaitDeadlinePolicyCancel {
		t.Errorf("policy = %q, want cancel", cfg.Runtime.Execution.WaitDeadlinePolicy)
	}
	if cfg.Runtime.Execution.MaxLifetime != "24h" {
		t.Errorf("max lifetime = %q, want 24h", cfg.Runtime.Execution.MaxLifetime)
	}
	if cfg.Runtime.Execution.TimerPoll.Interval != "2s" {
		t.Errorf("interval = %q, want 2s", cfg.Runtime.Execution.TimerPoll.Interval)
	}
	if cfg.Runtime.Execution.TimerPoll.BatchSize != 32 {
		t.Errorf("batch size = %d, want 32", cfg.Runtime.Execution.TimerPoll.BatchSize)
	}
}

func TestOperationalPolicyInvalidValuesFailWithActionableMessages(t *testing.T) {
	cases := map[string]struct {
		cli  map[string]any
		want []string
	}{
		"unknown deadline policy": {
			cli:  map[string]any{"runtime.execution.waitDeadlinePolicy": "later"},
			want: []string{"runtime.execution.waitDeadlinePolicy", "wait deadline policy", "later", "fail", "cancel", "continue"},
		},
		"unparsable max lifetime": {
			cli:  map[string]any{"runtime.execution.maxLifetime": "forever"},
			want: []string{"runtime.execution.maxLifetime", "execution max lifetime", "forever"},
		},
		"negative max lifetime": {
			cli:  map[string]any{"runtime.execution.maxLifetime": "-1h"},
			want: []string{"runtime.execution.maxLifetime", "negative"},
		},
		"unparsable poll interval": {
			cli:  map[string]any{"runtime.execution.timerPoll.interval": "often"},
			want: []string{"runtime.execution.timerPoll.interval", "timer poll interval", "often"},
		},
		"sub-minimum poll interval": {
			cli:  map[string]any{"runtime.execution.timerPoll.interval": "1ns"},
			want: []string{"runtime.execution.timerPoll.interval", "at least", core.MinTimerPollInterval.String()},
		},
		"excessive poll interval": {
			cli:  map[string]any{"runtime.execution.timerPoll.interval": "2m"},
			want: []string{"runtime.execution.timerPoll.interval", "must not exceed"},
		},
		"negative batch size": {
			cli:  map[string]any{"runtime.execution.timerPoll.batchSize": -3},
			want: []string{"runtime.execution.timerPoll.batchSize", "timer poll batch size", "-3"},
		},
		"excessive batch size": {
			cli:  map[string]any{"runtime.execution.timerPoll.batchSize": core.MaxTimerPollBatchSize + 1},
			want: []string{"runtime.execution.timerPoll.batchSize", "4097"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(Options{
				GlobalPath: "/nonexistent/global.yaml",
				ProjectDir: t.TempDir(),
				CLI:        tc.cli,
			})
			if err == nil {
				t.Fatalf("expected %v to be rejected", tc.cli)
			}
			for _, fragment := range tc.want {
				if !strings.Contains(err.Error(), fragment) {
					t.Errorf("error %q does not mention %q", err, fragment)
				}
			}
		})
	}
}

func TestProjectFileRejectsOperationalPolicy(t *testing.T) {
	// A denial message is part of the public configuration contract: it must
	// name only authoring surfaces that actually reach the runtime. The `neuron`
	// CLI has no operational-policy flags — only `nore` does — so advertising
	// `--flag` forms here would send users to a nonexistent surface.
	cases := map[string]struct {
		content string
		path    string
		env     string
		bogus   string
	}{
		"wait deadline policy": {
			content: "runtime:\n  execution:\n    waitDeadlinePolicy: continue\n",
			path:    "runtime.execution.waitDeadlinePolicy",
			env:     "NEURON_WAIT_DEADLINE_POLICY",
			bogus:   "--wait-deadline-policy",
		},
		"max lifetime": {
			content: "runtime:\n  execution:\n    maxLifetime: 24h\n",
			path:    "runtime.execution.maxLifetime",
			env:     "NEURON_EXECUTION_MAX_LIFETIME",
			bogus:   "--execution-max-lifetime",
		},
		"timer poll": {
			content: "runtime:\n  execution:\n    timerPoll:\n      interval: 5s\n",
			path:    "runtime.execution.timerPoll",
			env:     "NEURON_TIMER_POLL_INTERVAL",
			bogus:   "--timer-poll-interval",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := write(t, t.TempDir(), "neuron.config.yaml", tc.content)
			_, err := Load(Options{
				GlobalPath:  "/nonexistent/global.yaml",
				ProjectPath: path,
			})
			if err == nil {
				t.Fatal("expected the project file to reject the operational setting")
			}
			for _, fragment := range []string{tc.path, "deployment-level", "global config", tc.env} {
				if !strings.Contains(err.Error(), fragment) {
					t.Errorf("error %q does not mention %q", err, fragment)
				}
			}
			if strings.Contains(err.Error(), tc.bogus) {
				t.Errorf("error %q advertises the nonexistent neuron flag %q", err, tc.bogus)
			}
		})
	}
}
