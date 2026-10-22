package manifest

import (
	"reflect"
	"testing"

	"github.com/neuron-runtime/neuron/application/project"
	"github.com/neuron-runtime/neuron/shared/types/core"
)

// resolvedProjectWithResult builds a minimal resolved YAML project whose
// assembly declares a result contract: a schema plus fields sourced from a
// capability result, the assembly params, and a literal.
func resolvedProjectWithResult() *project.ResolvedProject {
	return &project.ResolvedProject{
		Assembly: project.ResolvedAssembly{
			Definition: project.AssemblyFile{
				APIVersion: "neuron/v1",
				Kind:       "Assembly",
				Metadata:   project.AssemblyMetadata{Name: "order-processing", Version: "1.0.0"},
				Result: &core.AssemblyResult{
					Schema: []core.Port{
						{Name: "order_id", Type: "string"},
						{Name: "status", Type: "string", Required: true},
					},
					Fields: []core.AssemblyResultField{
						{Name: "order_id", Source: &core.ValueRef{
							Kind: core.ValueRefAssemblyParams,
							Path: []string{"order", "id"},
						}},
						{Name: "status", Source: &core.ValueRef{
							Kind:  core.ValueRefLiteral,
							Value: "confirmed",
						}},
					},
				},
			},
		},
	}
}

func TestFromResolvedProjectConvertsTheResultContract(t *testing.T) {
	got := FromResolvedProject(resolvedProjectWithResult())
	if got == nil {
		t.Fatal("FromResolvedProject returned nil")
	}
	if got.Result == nil {
		t.Fatal("the YAML result contract was dropped by FromResolvedProject")
	}

	want := &AssemblyResult{
		Schema: []Port{
			{Name: "order_id", Type: "string"},
			{Name: "status", Type: "string", Required: true},
		},
		Fields: []AssemblyResultField{
			{Name: "order_id", Source: &core.ValueRef{
				Kind: core.ValueRefAssemblyParams,
				Path: []string{"order", "id"},
			}},
			{Name: "status", Source: &core.ValueRef{
				Kind:  core.ValueRefLiteral,
				Value: "confirmed",
			}},
		},
	}
	if !reflect.DeepEqual(got.Result, want) {
		t.Errorf("result = %+v, want %+v", got.Result, want)
	}
}

func TestFromResolvedProjectWithoutAResultContractStaysNil(t *testing.T) {
	rp := resolvedProjectWithResult()
	rp.Assembly.Definition.Result = nil

	got := FromResolvedProject(rp)
	if got == nil {
		t.Fatal("FromResolvedProject returned nil")
	}
	if got.Result != nil {
		t.Fatalf("result = %+v, want nil when the YAML assembly declares none", got.Result)
	}
}
