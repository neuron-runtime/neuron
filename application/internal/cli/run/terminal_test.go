package run

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// TestTerminalOutcomeReportsFailure covers the contract that makes `neuron run`
// usable from a script: the final execution event decides the command's result.
//
// The renderer already shows the failure to a human, but a process that always
// exits zero cannot be used in CI or a shell pipeline, so the failure has to
// reach the exit status.
func TestTerminalOutcomeReportsFailure(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		message   string
		wantErr   bool
		wantIn    string
	}{
		{
			name:      "completed execution succeeds",
			eventType: "execution.completed",
			wantErr:   false,
		},
		{
			name:      "failed execution errors",
			eventType: "execution.failed",
			message:   "binding source.result.order failed: no such key: order",
			wantErr:   true,
			wantIn:    "no such key: order",
		},
		{
			name:      "failed execution without a message still errors",
			eventType: "execution.failed",
			wantErr:   true,
			wantIn:    "execution failed",
		},
		{
			name:      "cancelled execution errors",
			eventType: "execution.cancelled",
			wantErr:   true,
			wantIn:    "execution cancelled",
		},
		{
			name:      "empty stream is not a success",
			eventType: "",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := []byte(nil)
			if tt.message != "" {
				var err error
				payload, err = json.Marshal(struct {
					Message string `json:"Message"`
				}{Message: tt.message})
				if err != nil {
					t.Fatal(err)
				}
			}

			err := terminalOutcome(protocol.StreamEvent{
				Type:    tt.eventType,
				Payload: payload,
			})

			if tt.wantErr {
				if err == nil {
					t.Fatal("terminalOutcome() = nil, want an error so the command exits non-zero")
				}
				if tt.wantIn != "" && !strings.Contains(err.Error(), tt.wantIn) {
					t.Errorf("error = %q, want it to mention %q", err, tt.wantIn)
				}
				return
			}

			if err != nil {
				t.Errorf("terminalOutcome() = %v, want nil", err)
			}
		})
	}
}
