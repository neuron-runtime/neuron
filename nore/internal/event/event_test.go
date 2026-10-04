package event

import "testing"

// TestCapabilityFailedMessageAcceptsBothShapes guards the reason this helper
// exists. A pointer payload used to fall past a single-form type assertion and
// report a generic failure, so the real cause of the failure was discarded
// before it reached the log or the failed-execution message.
func TestCapabilityFailedMessageAcceptsBothShapes(t *testing.T) {
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
			got, ok := CapabilityFailedMessage(tc.payload)
			if got != tc.want {
				t.Errorf("message = %q, want %q", got, tc.want)
			}
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
		})
	}
}
