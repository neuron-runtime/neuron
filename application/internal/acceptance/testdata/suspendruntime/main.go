// Command suspendruntime is a stdio JSON capability runtime used by the
// acceptance suite. It suspends on the first invocation and completes on
// resume, allowing tests to exercise durable wait, restart, signal delivery,
// and timer recovery through the real CLI/runtime path.
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

		if marker, ok := req.Params["marker"].(string); ok && marker != "" {
			_ = os.WriteFile(marker, []byte("started"), 0o644)
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

		waitType, _ := req.Params["wait_type"].(string)
		if waitType == "" {
			waitType = "signal"
		}

		s := &suspend{
			CorrelationKey: stringParam(req.Params, "correlation_key"),
			WaitType:       waitType,
			ExpectedSignal: stringParam(req.Params, "expected_signal"),
			PayloadSchema:  stringParam(req.Params, "payload_schema"),
		}
		if waitType == "timer" {
			delayMS := int64(250)
			if v, ok := req.Params["timer_delay_ms"].(float64); ok && v > 0 {
				delayMS = int64(v)
			}
			s.DeadlineUnixMs = time.Now().Add(time.Duration(delayMS) * time.Millisecond).UnixMilli()
			if s.DeadlineUnixMs <= 0 {
				s.DeadlineUnixMs = time.Now().Add(250 * time.Millisecond).UnixMilli()
			}
		}
		if s.ExpectedSignal == "" && waitType == "signal" {
			s.ExpectedSignal = "approve"
		}

		_ = encoder.Encode(response{Suspend: s})
		return
	}
}

func stringParam(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}
