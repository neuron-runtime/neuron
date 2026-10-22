package execution

import (
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

// resultContractBlueprint builds a blueprint whose Assembly result contract
// exercises every resolvable source kind: a capability result, the assembly's
// initial params, a capability's own params, and a literal.
func resultContractBlueprint() *types.ExecutionBlueprint {
	return &types.ExecutionBlueprint{
		Metadata: shared.Metadata{ID: "bp", Name: "result-contract", Version: "1"},
		Nodes: map[shared.ID]types.ExecutionNode{
			"say": {Capability: shared.Capability{Metadata: shared.Metadata{ID: "say"}}},
		},
		EntryCapabilityIDs: []shared.ID{"say"},
		Result: &types.ExecutionResult{
			Schema: []shared.Port{
				{Name: "name", Type: "string"},
				{Name: "order_id", Type: "string"},
				{Name: "status", Type: "string"},
				{Name: "typed", Type: "string"},
			},
			Fields: []types.ExecutionResultField{
				{Name: "name", Source: &shared.ValueRef{
					Kind:       shared.ValueRefCapabilityResult,
					Capability: "say",
					Path:       []string{"name"},
				}},
				{Name: "order_id", Source: &shared.ValueRef{
					Kind: shared.ValueRefAssemblyParams,
					Path: []string{"order", "id"},
				}},
				{Name: "status", Source: &shared.ValueRef{
					Kind:  shared.ValueRefLiteral,
					Value: "confirmed",
				}},
				{Name: "typed", Source: &shared.ValueRef{
					Kind:       shared.ValueRefCapabilityParams,
					Capability: "say",
					Path:       []string{"name"},
				}},
			},
		},
	}
}

// startedResultExecution drives an execution through the lifecycle far enough
// for every result source kind to be resolvable: initial params are set by
// Start and the capability result is set by MarkCapabilityCompleted.
func startedResultExecution(t *testing.T) *Execution {
	t.Helper()
	e, err := NewExecution(resultContractBlueprint(), "corr_1", "inst_1")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := e.Start(map[string]any{
		"order": map[string]any{"id": "ord_1001"},
	}, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := e.MarkCapabilityReady("say", map[string]any{"name": "World"}); err != nil {
		t.Fatalf("MarkCapabilityReady: %v", err)
	}
	if err := e.MarkCapabilityRunning("say"); err != nil {
		t.Fatalf("MarkCapabilityRunning: %v", err)
	}
	if err := e.MarkCapabilityCompleted("say", map[string]any{"name": "World"}); err != nil {
		t.Fatalf("MarkCapabilityCompleted: %v", err)
	}
	return e
}

func TestResolveAssemblyResultAssemblesEverySourceKind(t *testing.T) {
	e := startedResultExecution(t)

	got, err := e.ResolveAssemblyResult()
	if err != nil {
		t.Fatalf("ResolveAssemblyResult: %v", err)
	}

	want := map[string]any{
		"name":     "World",
		"order_id": "ord_1001",
		"status":   "confirmed",
		"typed":    "World",
	}
	if len(got) != len(want) {
		t.Fatalf("ResolveAssemblyResult = %+v, want %+v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("result[%q] = %#v, want %#v", k, got[k], v)
		}
	}
}

func TestResolveAssemblyResultWithoutAContractIsNil(t *testing.T) {
	e, err := NewExecution(&types.ExecutionBlueprint{
		Metadata: shared.Metadata{ID: "bp", Name: "no-result", Version: "1"},
		Nodes: map[shared.ID]types.ExecutionNode{
			"say": {Capability: shared.Capability{Metadata: shared.Metadata{ID: "say"}}},
		},
		EntryCapabilityIDs: []shared.ID{"say"},
	}, "corr_1", "inst_1")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := e.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	got, err := e.ResolveAssemblyResult()
	if err != nil {
		t.Fatalf("ResolveAssemblyResult: %v", err)
	}
	if got != nil {
		t.Fatalf("ResolveAssemblyResult = %+v, want nil when no result contract is declared", got)
	}
}

func TestResolveAssemblyResultReportsAnUnresolvableCapabilityResult(t *testing.T) {
	e, err := NewExecution(resultContractBlueprint(), "corr_1", "inst_1")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := e.Start(map[string]any{
		"order": map[string]any{"id": "ord_1001"},
	}, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// The capability never completed, so its result is absent.
	if _, err := e.ResolveAssemblyResult(); err == nil {
		t.Fatal("a result field reading an absent capability result must fail")
	}
}
