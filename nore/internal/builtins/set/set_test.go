package set

import (
	"context"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
)

func TestSetMergesConfigurationAndParams(t *testing.T) {
	result, err := Set{}.Execute(context.Background(), contracts.ExecutionContext{
		CapabilityConfigurations: map[string]any{"region": "eu", "retries": float64(3)},
		Params:                   map[string]any{"region": "us"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result["region"] != "us" {
		t.Errorf("region = %v, want the param to override the configuration", result["region"])
	}
	if result["retries"] != float64(3) {
		t.Errorf("retries = %v, want the configuration value", result["retries"])
	}
}

func TestSetReturnsACopy(t *testing.T) {
	configuration := map[string]any{"region": "eu"}
	result, err := Set{}.Execute(context.Background(), contracts.ExecutionContext{
		CapabilityConfigurations: configuration,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	result["region"] = "changed"
	result["added"] = true

	if configuration["region"] != "eu" {
		t.Errorf("the configuration was mutated: %v", configuration)
	}
	if _, ok := configuration["added"]; ok {
		t.Errorf("the configuration gained a key: %v", configuration)
	}
}

func TestSetReportsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := (Set{}).Execute(ctx, contracts.ExecutionContext{}); err == nil {
		t.Fatal("Execute succeeded for a cancelled execution")
	}
}

// The format is an instruction to the built-in rather than a value of the
// set, so it is not returned as one.
func TestSetDoesNotReturnTheFormat(t *testing.T) {
	result, err := Set{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"region": "us", "format": "json"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, ok := result["format"]; ok {
		t.Errorf("the result carries the format: %v", result)
	}
	if result["region"] != "us" {
		t.Errorf("region = %v, want us", result["region"])
	}
}

// The text format keeps the names of the set and represents the values as
// text, so the shape of the result does not change between the formats.
func TestSetRendersTheValuesAsText(t *testing.T) {
	result, err := Set{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{
			"format": "text",
			"name":   "World",
			"count":  float64(42),
			"ready":  true,
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result["name"] != "World" {
		t.Errorf("name = %#v, want the text of the value", result["name"])
	}
	if result["count"] != "42" {
		t.Errorf("count = %#v, want the text of the value", result["count"])
	}
	if result["ready"] != "true" {
		t.Errorf("ready = %#v, want the text of the value", result["ready"])
	}
}

// A format that does not exist is refused rather than silently ignored.
func TestSetRefusesAnUnknownFormat(t *testing.T) {
	_, err := Set{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"format": "yaml"},
	})
	if err == nil {
		t.Fatal("Execute accepted a format that does not exist")
	}
}
