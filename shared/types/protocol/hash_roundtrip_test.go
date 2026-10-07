package protocol

import (
	"encoding/json"
	"testing"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

func TestHashRoundTripStableWithStructuredRefs(t *testing.T) {
	a := core.Assembly{
		Metadata: core.Metadata{ID: "assembly_abc", Name: "order-processing", Version: "1.0.0"},
		Specification: core.AssemblySpec{
			Capabilities: []core.Capability{
				{Metadata: core.Metadata{ID: "v", Name: "v"}, Type: "neuron:core:set"},
				{Metadata: core.Metadata{ID: "p", Name: "p"}, Type: "neuron:core:set"},
			},
			Bindings: []core.Binding{{
				Metadata: core.Metadata{ID: "binding_abc", Name: "v->p"},
				From: core.Endpoint{CapabilityID: "v"},
				To: core.Endpoint{CapabilityID: "p"},
				Mappings: []core.MappingRule{
					{TargetPath: "validation_data", Source: &core.ValueRef{Kind: core.ValueRefCapabilityResult, Capability: "v", Path: []string{"order", "customer_id"}}},
				},
			}},
		},
	}
	h1, err := HashAssembly(a)
	if err != nil { t.Fatal(err) }
	data, err := json.Marshal(a)
	if err != nil { t.Fatal(err) }
	t.Logf("wire: %s", data)
	var decoded core.Assembly
	if err := json.Unmarshal(data, &decoded); err != nil { t.Fatal(err) }
	h2, err := HashAssembly(decoded)
	if err != nil { t.Fatal(err) }
	if h1 != h2 { t.Fatalf("hash unstable after JSON round trip: %s != %s", h1, h2) }
}
