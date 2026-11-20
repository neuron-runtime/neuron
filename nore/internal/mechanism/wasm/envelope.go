package wasm

import (
	"encoding/json"
	"errors"
	"fmt"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// This file owns the additive suspend/resume handling for the stdin/stdout JSON
// envelope used by WASI capability runtimes. Request.resume and Response.suspend
// are optional fields (see shared/types/capabilityruntime/suspend.go), so an old
// module's JSON decodes with Resume/Suspend == nil and behaves exactly as before.

// encodeRequest serializes a capability request for the WASI stdin stream. It
// bounds the additive resume input before encoding so host-supplied wait data
// cannot expand into unbounded work inside the sandbox.
func encodeRequest(req *capabilityrt.Request) ([]byte, error) {
	if req == nil {
		req = &capabilityrt.Request{}
	}
	if err := capabilityrt.ValidateResumeInput(req.Resume); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode capability request: %w", err)
	}
	return encoded, nil
}

// decodeResponse parses a WASI stdout response. Suspend metadata is untrusted
// executor output, so it is validated here, at the transport boundary, before
// the response is handed onward.
//
// The stdin/stdout protocol has no Initialize handshake: emitting a valid,
// well-formed suspend field is itself the runtime's opt-in to the extension. A
// runtime that predates suspend cannot emit the field, so its response decodes
// with Suspend == nil and is handled exactly as before.
func decodeResponse(data []byte) (*capabilityrt.Response, error) {
	var resp capabilityrt.Response
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode capability response: %w", err)
	}
	if resp.Suspend != nil {
		if err := capabilityrt.ValidateSuspend(resp.Suspend); err != nil {
			return nil, err
		}
		if len(resp.Result) != 0 || resp.Error != "" {
			return nil, errors.New("invalid_suspend_outcome: suspend must not be combined with a non-empty result or error")
		}
	}
	return &resp, nil
}
