package event

import "testing"

// TestMessageReadsEveryReasonCarryingForm guards the reason this accessor
// exists. A pointer payload used to fall past a single-form type assertion and
// report a generic failure, so the real cause of the failure was discarded
// before it reached the log or the failed-execution message.
//
// Every reason-carrying payload is covered in both the value and the pointer
// form, because a payload travels through an `any` field and either is a
// legitimate thing for a producer to publish.
func TestMessageReadsEveryReasonCarryingForm(t *testing.T) {
	cases := []struct {
		name    string
		payload any
		want    string
		wantOK  bool
	}{
		{
			name:    "value",
			payload: CapabilityFailedPayload{Message: "boom"},
			want:    "boom",
			wantOK:  true,
		},
		{
			name:    "pointer",
			payload: &CapabilityFailedPayload{Message: "boom"},
			want:    "boom",
			wantOK:  true,
		},
		{
			name:    "pointer to value payload",
			payload: &CapabilityCancelledPayload{Message: "stopped by a sibling"},
			want:    "stopped by a sibling",
			wantOK:  true,
		},
		{
			name:    "cancelled capability payload",
			payload: CapabilityCancelledPayload{Message: "stopped by a sibling"},
			want:    "stopped by a sibling",
			wantOK:  true,
		},
		{
			name:    "execution failed payload",
			payload: ExecutionFailedPayload{Message: "the run broke"},
			want:    "the run broke",
			wantOK:  true,
		},
		{
			name:    "pointer to execution failed payload",
			payload: &ExecutionFailedPayload{Message: "the run broke"},
			want:    "the run broke",
			wantOK:  true,
		},
		{
			name:    "execution cancelled payload",
			payload: ExecutionCancelledPayload{Message: "stopped by operator"},
			want:    "stopped by operator",
			wantOK:  true,
		},
		{
			name:    "bare string",
			payload: "boom",
			want:    "boom",
			wantOK:  true,
		},
		{
			// An empty message is still a message. Reporting it as absent would
			// make the scheduler invent a generic reason for a failure that
			// deliberately carried none.
			name:    "empty message",
			payload: CapabilityFailedPayload{},
			want:    "",
			wantOK:  true,
		},
		{
			name:    "nil pointer",
			payload: (*CapabilityFailedPayload)(nil),
			want:    "",
			wantOK:  false,
		},
		{
			name:    "unrelated payload",
			payload: 42,
			want:    "",
			wantOK:  false,
		},
		{
			name:    "nil",
			payload: nil,
			want:    "",
			wantOK:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Message(tc.payload)
			if got != tc.want {
				t.Errorf("message = %q, want %q", got, tc.want)
			}
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
		})
	}
}
