package protocol

// Response Base wrapper to standardize all API replies
type Response struct {
	Message string `json:"message"`
	Status  int    `json:"status"`
	Data    any    `json:"data,omitempty"`

	// Code is a stable, machine-readable outcome identifier. It is set on
	// error responses so a caller can distinguish outcomes that share an HTTP
	// status (for example a conflicting signal delivery from an ambiguous
	// wait) without parsing the human-readable Message.
	Code string `json:"code,omitempty"`
}

// Signal error codes. They are part of the HTTP contract: the client that
// retries a delivery or narrows an ambiguous wait keys off these values, not
// off the message text.
const (
	SignalCodeConflict       = "signal_delivery_conflict"
	SignalCodeAmbiguousWait  = "ambiguous_wait"
	SignalCodeNoMatchingWait = "no_matching_wait"
	SignalCodeExpired        = "signal_expired"
	SignalCodeNotFound       = "execution_not_found"
	SignalCodeInvalidRequest = "invalid_signal_request"
	SignalCodeInvalidPayload = "invalid_signal_payload"
	SignalCodeUnavailable    = "signal_unavailable"
)
