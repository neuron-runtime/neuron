package capabilityruntime

import (
	"encoding/json"
	"errors"
	"fmt"
)

// This file defines the additive suspend/resume fields of the stdin/stdout JSON
// capability-runtime envelope. Request and Response (protocol.go) are otherwise
// unchanged: both new fields are pointers tagged omitempty, so an old runtime
// that never emits (or never reads) them observes byte-for-byte the same wire
// it did before this extension. A runtime that predates suspend cannot emit a
// Suspend, and a host that predates resume cannot emit a ResumeInput.

// Wait type values carried on the wire. These mirror the gRPC SuspendOutcome
// vocabulary in shared/protocol/capabilityruntime/v1; "deadline" is deliberately
// absent because it is an engine-internal policy, not an executor choice.
const (
	WaitTypeSignal = "signal"
	WaitTypeTimer  = "timer"
)

// Resume cause values carried on the wire.
const (
	ResumeCauseSignal   = "signal"
	ResumeCauseTimer    = "timer"
	ResumeCauseDeadline = "deadline"
)

// Bounds applied to executor-supplied suspend and resume metadata at the
// transport boundary. Suspend metadata is untrusted: a capability runtime is
// arbitrary code and its declarations are not permissions. These limits keep a
// malicious or broken runtime from forcing unbounded allocation or handing an
// oversized value to the durable wait store.
const (
	maxMetadataFieldBytes     = 256
	maxResumeCauseBytes       = 64
	maxResumePayloadEntries   = 64
	maxResumePayloadKeyBytes  = 256
	maxResumePayloadValueJSON = 4096
)

// Suspend is the additive suspend outcome carried in Response. It is the JSON
// envelope counterpart of the gRPC SuspendOutcome; DeadlineUnixMs is the wire
// representation of the wait deadline (Unix milliseconds, 0 means no deadline).
type Suspend struct {
	CorrelationKey string `json:"correlationKey,omitempty"`
	WaitType       string `json:"waitType,omitempty"`
	ExpectedSignal string `json:"expectedSignal,omitempty"`
	DeadlineUnixMs int64  `json:"deadlineUnixMs,omitempty"`
	PayloadSchema  string `json:"payloadSchema,omitempty"`
}

// ResumeInput is the additive resume input carried in Request. It is the JSON
// envelope counterpart of the gRPC ResumeInput. DeliveryID is the client's
// idempotency key: the transport carries it verbatim and never dedupes it, so
// repeated delivery reaches the capability runtime unchanged.
type ResumeInput struct {
	Cause      string         `json:"cause,omitempty"`
	SignalName string         `json:"signalName,omitempty"`
	DeliveryID string         `json:"deliveryId,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
}

// ValidateSuspend checks untrusted suspend metadata at the transport boundary.
// It returns an explicit error rather than letting an invalid outcome become an
// empty success. The engine's EnterWait remains the authoritative domain
// validator; this check only rejects values that cannot be safely mapped into
// the domain types.
func ValidateSuspend(s *Suspend) error {
	if s == nil {
		return errors.New("invalid_suspend_outcome: nil suspend")
	}
	if err := checkFieldLength("invalid_suspend_outcome", "correlation_key", s.CorrelationKey); err != nil {
		return err
	}
	if err := checkFieldLength("invalid_suspend_outcome", "payload_schema", s.PayloadSchema); err != nil {
		return err
	}

	switch s.WaitType {
	case WaitTypeSignal:
		if s.ExpectedSignal == "" {
			return errors.New("invalid_suspend_outcome: signal wait requires a non-empty expected_signal")
		}
	case WaitTypeTimer:
		if s.DeadlineUnixMs <= 0 {
			return errors.New("invalid_suspend_outcome: timer wait requires deadline_unix_ms > 0")
		}
	default:
		return fmt.Errorf("invalid_suspend_outcome: wait_type must be %q or %q", WaitTypeSignal, WaitTypeTimer)
	}

	if err := checkFieldLength("invalid_suspend_outcome", "expected_signal", s.ExpectedSignal); err != nil {
		return err
	}
	if s.DeadlineUnixMs < 0 {
		return errors.New("invalid_suspend_outcome: deadline_unix_ms must not be negative")
	}
	return nil
}

// ValidateResumeInput bounds untrusted resume input before it is handed to a
// capability runtime. Resume inputs originate from the host's wait store rather
// than the executor, but they still cross a trust boundary and are bounded here
// so a corrupt store record cannot expand into unbounded work.
func ValidateResumeInput(r *ResumeInput) error {
	if r == nil {
		return nil
	}
	if len(r.Cause) > maxResumeCauseBytes {
		return errors.New("invalid_resume_input: cause exceeds the allowed length")
	}
	if err := checkFieldLength("invalid_resume_input", "signal_name", r.SignalName); err != nil {
		return err
	}
	if err := checkFieldLength("invalid_resume_input", "delivery_id", r.DeliveryID); err != nil {
		return err
	}
	if len(r.Payload) > maxResumePayloadEntries {
		return errors.New("invalid_resume_input: payload has too many entries")
	}
	for key, value := range r.Payload {
		if len(key) > maxResumePayloadKeyBytes {
			return errors.New("invalid_resume_input: payload key exceeds the allowed length")
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("invalid_resume_input: payload value %q is not encodable: %w", key, err)
		}
		if len(encoded) > maxResumePayloadValueJSON {
			return errors.New("invalid_resume_input: payload value exceeds the allowed size")
		}
	}
	return nil
}

func checkFieldLength(prefix, name, value string) error {
	if len(value) > maxMetadataFieldBytes {
		return fmt.Errorf("%s: %s exceeds the allowed length", prefix, name)
	}
	return nil
}
