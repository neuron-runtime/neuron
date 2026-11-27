// Command workerdeadline is a stdio JSON capability runtime used by the
// acceptance suite. It extends the suspend fixture with an explicit deadline on
// signal waits so deployment deadline policies can be exercised end to end. It
// suspends on the first invocation and completes on resume, recording the resume
// cause to a marker file.
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"time"
)

type request struct {
	Params map[string]any `json:"params"`
	Resume *resumeInput   `json:"resume,omitempty"`
}

type resumeInput struct {
	Cause      string         `json:"cause,omitempty"`
	SignalName string         `json:"signalName,omitempty"`
	DeliveryID string         `json:"deliveryId,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
}

type response struct {
	Result  map[string]any `json:"result,omitempty"`
	Error   string         `json:"error,omitempty"`
	Suspend *suspend       `json:"suspend,omitempty"`
}

type suspend struct {
	CorrelationKey string `json:"correlationKey,omitempty"`
	WaitType       string `json:"waitType,omitempty"`
	ExpectedSignal string `json:"expectedSignal,omitempty"`
	DeadlineUnixMs int64  `json:"deadlineUnixMs,omitempty"`
	PayloadSchema  string `json:"payloadSchema,omitempty"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	encoder := json.NewEncoder(os.Stdout)

	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}

		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			_ = encoder.Encode(response{Error: err.Error()})
			return
		}

		if req.Resume != nil {
			payload := req.Resume.Payload
			if payload == nil {
				payload = map[string]any{}
			}
			result := map[string]any{
				"done":        true,
				"cause":       req.Resume.Cause,
				"signal":      req.Resume.SignalName,
				"delivery_id": req.Resume.DeliveryID,
				"payload":     payload,
			}
			if resumeMarker, ok := req.Params["resume_marker"].(string); ok && resumeMarker != "" {
				data, err := json.Marshal(result)
				if err != nil {
					_ = encoder.Encode(response{Error: err.Error()})
					return
				}
				if err := os.WriteFile(resumeMarker, data, 0o644); err != nil {
					_ = encoder.Encode(response{Error: err.Error()})
					return
				}
			}
			_ = encoder.Encode(response{Result: result})
			return
		}

		waitType := stringParam(req.Params, "wait_type")
		if waitType == "" {
			waitType = "signal"
		}
		s := &suspend{
			CorrelationKey: stringParam(req.Params, "correlation_key"),
			WaitType:       waitType,
			ExpectedSignal: stringParam(req.Params, "expected_signal"),
			PayloadSchema:  stringParam(req.Params, "payload_schema"),
		}
		if s.ExpectedSignal == "" && waitType == "signal" {
			s.ExpectedSignal = "approve"
		}
		// A positive deadline_ms gives signal waits a deadline, which is what
		// makes the deployment deadline policy observable. The value is
		// author-supplied test data, not a runtime policy of its own.
		if delayMS := intParam(req.Params, "deadline_ms"); delayMS > 0 {
			s.DeadlineUnixMs = time.Now().Add(time.Duration(delayMS) * time.Millisecond).UnixMilli()
		}

		_ = encoder.Encode(response{Suspend: s})
		return
	}
}

func stringParam(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}

func intParam(params map[string]any, key string) int64 {
	v, _ := params[key].(float64)
	return int64(v)
}
