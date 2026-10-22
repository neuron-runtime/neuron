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
