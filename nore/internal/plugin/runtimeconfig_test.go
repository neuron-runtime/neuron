package plugin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// recordingInstance captures the request a capability runtime actually receives.
type recordingInstance struct {
	request  *capabilityrt.Request
	response *capabilityrt.Response
}

func (r *recordingInstance) Execute(_ context.Context, req *capabilityrt.Request) (*capabilityrt.Response, error) {
	r.request = req
	if r.response != nil {
		return r.response, nil
	}
	return &capabilityrt.Response{Result: map[string]any{"ok": true}}, nil
}

func (r *recordingInstance) Health(context.Context) error { return nil }
func (r *recordingInstance) Close(context.Context) error  { return nil }

func TestAdapterSendsOnlyCapabilityParamsToTheRuntime(t *testing.T) {
	// A runtimeConfig is an instruction to N.O.R.E. It must never be presented
	// to the capability runtime as capability input.
	instance := &recordingInstance{}
	adapter := &instanceAdapter{instance: instance, typ: "acme:http"}

	runtimeConfig := &core.RuntimeConfig{
		Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeDetach, Timeout: "45s"},
		Retry:     &core.RuntimeRetry{Policy: core.RetryPolicyExponential, MaxAttempts: 3},
	}

	_, err := adapter.Execute(context.Background(), contracts.ExecutionContext{
		ExecutionID:   core.ID("exec"),
		CorrelationID: core.ID("corr"),
		Capability: core.Capability{
			Metadata:      core.Metadata{ID: core.ID("call"), Name: "call"},
			Type:          "acme:http",
			RuntimeConfig: runtimeConfig,
		},
		Params:                   map[string]any{"url": "https://example.test"},
		CapabilityConfigurations: map[string]any{"headers": map[string]any{"accept": "application/json"}},
		RuntimeConfig:            runtimeConfig,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if instance.request == nil {
		t.Fatal("the runtime received no request")
	}

	if got := instance.request.Params["url"]; got != "https://example.test" {
		t.Fatalf("params were not forwarded: url = %v", got)
	}

	// The runtime's own capability must never see engine-level configuration.
	if len(instance.request.Params) != 1 {
		t.Fatalf("the runtime must receive only capability params, got %v", instance.request.Params)
	}
	for _, leaked := range []string{"runtimeConfig", "execution", "retry", "resources", "mode", "timeout"} {
		if _, present := instance.request.Params[leaked]; present {
			t.Fatalf("runtime configuration leaked into capability params as %q", leaked)
		}
	}
}

// The wire payload is the boundary an external runtime actually observes, so
// assert against the serialized form rather than only the Go value.
func TestAdapterWirePayloadCarriesNoRuntimeConfiguration(t *testing.T) {
	instance := &recordingInstance{}
	adapter := &instanceAdapter{instance: instance, typ: "acme:http"}

	runtimeConfig := &core.RuntimeConfig{
		Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeDetach, Timeout: "45s"},
		Retry:     &core.RuntimeRetry{Policy: core.RetryPolicyExponential, MaxAttempts: 3},
	}
	_, err := adapter.Execute(context.Background(), contracts.ExecutionContext{
		Capability:    core.Capability{Type: "acme:http", RuntimeConfig: runtimeConfig},
		Params:        map[string]any{"url": "https://example.test"},
		RuntimeConfig: runtimeConfig,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	payload, err := json.Marshal(instance.request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	for _, forbidden := range []string{"runtimeConfig", "maxAttempts", "policy", "initialBackoff"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("serialized runtime request leaked %q: %s", forbidden, payload)
		}
	}
}
