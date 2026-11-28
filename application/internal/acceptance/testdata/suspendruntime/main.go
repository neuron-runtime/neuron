// Command suspendruntime is a gRPC capability runtime used by the acceptance
// suite. It suspends on the first invocation and completes on resume, allowing
// tests to exercise durable wait, restart, signal delivery, timer recovery, and
// ADR 0010 per-invocation identity through the real CLI/runtime path.
//
// The gRPC transport is required for identity evidence: the legacy stdin/stdout
// JSON Request has no attempt_id or logical_operation_id fields, while the
// canonical ExecuteRequest carries both. Markers are the stable acceptance
// boundary for observing those values without engine internals.
package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	capabilityruntime "github.com/neuron-runtime/neuron/packages/executor-sdks/golang"
	v1 "github.com/neuron-runtime/neuron/shared/protocol/capabilityruntime/v1"
)

func main() {
	err := capabilityruntime.Serve(capabilityruntime.Handler{
		Initialize: func(ctx context.Context, protocol string, metadata map[string]string) (*capabilityruntime.InitializeResult, error) {
			return &capabilityruntime.InitializeResult{
				ProtocolVersion: protocol,
				Features:        []string{v1.SuspendFeature},
			}, nil
		},
		ExecuteWithRequest: execute,
	})
	if err != nil {
		os.Exit(1)
	}
}

func execute(ctx context.Context, req capabilityruntime.ExecutionRequest) (*capabilityruntime.ExecutionResult, error) {
	if marker, ok := req.Params["marker"].(string); ok && marker != "" {
		if err := writeMarker(marker, map[string]any{
			"attempt_id":           req.AttemptID,
			"logical_operation_id": req.LogicalOperationID,
		}); err != nil {
			return nil, err
		}
	}

	if req.Resume != nil {
		payload := req.Resume.Payload
		if payload == nil {
			payload = map[string]any{}
		}
		result := map[string]any{
			"done":                 true,
			"cause":                req.Resume.Cause,
			"signal":               req.Resume.SignalName,
			"delivery_id":          req.Resume.DeliveryID,
			"payload":              payload,
			"attempt_id":           req.AttemptID,
			"logical_operation_id": req.LogicalOperationID,
		}
		if resumeMarker, ok := req.Params["resume_marker"].(string); ok && resumeMarker != "" {
			if err := writeMarker(resumeMarker, result); err != nil {
				return nil, err
			}
		}
		return &capabilityruntime.ExecutionResult{Result: result}, nil
	}

	waitType, _ := req.Params["wait_type"].(string)
	if waitType == "" {
		waitType = capabilityruntime.WaitTypeSignal
	}
	suspend := &capabilityruntime.SuspendOutcome{
		CorrelationKey: stringParam(req.Params, "correlation_key"),
		WaitType:       waitType,
		ExpectedSignal: stringParam(req.Params, "expected_signal"),
		PayloadSchema:  stringParam(req.Params, "payload_schema"),
	}
	if waitType == capabilityruntime.WaitTypeTimer {
		delayMS := int64(250)
		if v, ok := req.Params["timer_delay_ms"].(float64); ok && v > 0 {
			delayMS = int64(v)
		}
		suspend.DeadlineUnixMs = time.Now().Add(time.Duration(delayMS) * time.Millisecond).UnixMilli()
	}
	if suspend.ExpectedSignal == "" && waitType == capabilityruntime.WaitTypeSignal {
		suspend.ExpectedSignal = "approve"
	}
	return &capabilityruntime.ExecutionResult{Suspend: suspend}, nil
}

func writeMarker(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func stringParam(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}
