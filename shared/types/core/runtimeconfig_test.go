package core

import "testing"

func TestRuntimeConfigValidateAcceptsUnsetFields(t *testing.T) {
	cases := map[string]*RuntimeConfig{
		"nil":          nil,
		"empty":        {},
		"empty groups": {Execution: &RuntimeExecution{}, Retry: &RuntimeRetry{}, Resources: &RuntimeResources{}},
		"fully declared": {
			Execution: &RuntimeExecution{Mode: RuntimeExecutionModeDetach, Timeout: "45s"},
			Retry: &RuntimeRetry{
				Policy:         RetryPolicyExponential,
				MaxAttempts:    4,
				InitialBackoff: "100ms",
				MaxBackoff:     "2s",
			},
			Resources: &RuntimeResources{},
		},
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			if err := config.Validate(); err != nil {
				t.Fatalf("expected valid runtime config, got %v", err)
			}
		})
	}
}

func TestRuntimeConfigValidateRejectsUnknownValues(t *testing.T) {
	cases := map[string]*RuntimeConfig{
		"unknown mode": {
			Execution: &RuntimeExecution{Mode: RuntimeExecutionMode("detatch")},
		},
		"unparsable timeout": {
			Execution: &RuntimeExecution{Timeout: "5 seconds"},
		},
		"negative timeout": {
			Execution: &RuntimeExecution{Timeout: "-5s"},
		},
		"unknown retry policy": {
			Retry: &RuntimeRetry{Policy: RetryPolicy("linear")},
		},
		"negative max attempts": {
			Retry: &RuntimeRetry{Policy: RetryPolicyFixed, MaxAttempts: -1},
		},
		"max attempts without a policy": {
			Retry: &RuntimeRetry{MaxAttempts: 3},
		},
		"unparsable initial backoff": {
			Retry: &RuntimeRetry{Policy: RetryPolicyFixed, MaxAttempts: 2, InitialBackoff: "fast"},
		},
		"max backoff below initial backoff": {
			Retry: &RuntimeRetry{
				Policy:         RetryPolicyExponential,
				MaxAttempts:    2,
				InitialBackoff: "2s",
				MaxBackoff:     "100ms",
			},
		},
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			if err := config.Validate(); err == nil {
				t.Fatal("expected an invalid runtime config to be rejected")
			}
		})
	}
}

func TestParseRuntimeDuration(t *testing.T) {
	cases := map[string]struct {
		input   string
		wantErr bool
	}{
		"empty":     {input: ""},
		"seconds":   {input: "5s"},
		"minutes":   {input: "30m"},
		"compound":  {input: "1m30s"},
		"millis":    {input: "250ms"},
		"bare word": {input: "soon", wantErr: true},
		"negative":  {input: "-1s", wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseRuntimeDuration(tc.input)
			if tc.wantErr && err == nil {
				t.Fatalf("expected %q to be rejected", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected %q to parse, got %v", tc.input, err)
			}
		})
	}
}
