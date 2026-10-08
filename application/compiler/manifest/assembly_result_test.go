package manifest

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

// resultManifest is the canonical manifest shape the compiler must carry into
// core: assembly params, a declared result schema, and structured field
// sources drawn from capability results, assembly params, and a literal.
func resultManifest() *Assembly {
	return &Assembly{
		Params: []Port{
			{Name: "order", Type: "object", Required: true},
			{Name: "traceId", Type: "string"},
		},
		Result: &AssemblyResult{
			Schema: []Port{
				{Name: "order", Type: "object", Required: true},
				{Name: "trackingId", Type: "string", Required: true},
			},
			Fields: []AssemblyResultField{
				{Name: "order", Source: &core.ValueRef{
					Kind:       core.ValueRefCapabilityResult,
					Capability: "validate-order",
					Path:       []string{"order"},
				}},
				{Name: "trackingId", Source: &core.ValueRef{
					Kind:       core.ValueRefCapabilityResult,
					Capability: "create-shipment",
					Path:       []string{"tracking_id"},
				}},
			},
		},
	}
}

func TestAssemblyResultRoundTrip(t *testing.T) {
	want := resultManifest()

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got := &Assembly{}
	if err := json.Unmarshal(data, got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip changed the manifest:\ngot  %+v\nwant %+v", got, want)
	}

	// Field and schema order is the author's declared contract and must be
	// preserved verbatim through serialization.
	if len(got.Params) != 2 || got.Params[0].Name != "order" || got.Params[1].Name != "traceId" {
		t.Errorf("params order changed: %+v", got.Params)
	}
	if len(got.Result.Schema) != 2 || got.Result.Schema[0].Name != "order" || got.Result.Schema[1].Name != "trackingId" {
		t.Errorf("result schema order changed: %+v", got.Result.Schema)
	}
	if len(got.Result.Fields) != 2 {
		t.Fatalf("result fields = %d, want 2", len(got.Result.Fields))
	}
	if got.Result.Fields[0].Source == nil ||
		got.Result.Fields[0].Source.Kind != core.ValueRefCapabilityResult ||
		got.Result.Fields[0].Source.Capability != "validate-order" ||
		!reflect.DeepEqual(got.Result.Fields[0].Source.Path, []string{"order"}) {
		t.Errorf("first field source = %+v, want capabilityResult validate-order", got.Result.Fields[0].Source)
	}
}

func TestAssemblyResultRoundTripOmitsOmittedParamsAndNilResult(t *testing.T) {
	m := &Assembly{}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	text := string(data)
	if strings.Contains(text, `"params"`) {
		t.Errorf("manifest without params serialized %q", text)
	}
	if strings.Contains(text, `"result"`) {
		t.Errorf("manifest without result serialized %q", text)
	}
}

func TestCanonicalizeNormalizesAssemblyResultSourcePaths(t *testing.T) {
	m := resultManifest()
	m.Result.Fields = []AssemblyResultField{
		{Name: "order", Source: &core.ValueRef{
			Kind:       core.ValueRefCapabilityResult,
			Capability: "validate-order",
			Path:       []string{"order", "customerId"},
		}},
	}

	canonical, err := Canonicalize(m)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}

	got := canonical.Result.Fields[0].Source
	want := core.ValueRef{Kind: core.ValueRefCapabilityResult, Capability: "validate-order", Path: []string{"order", "customer_id"}}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("source = %+v, want %+v (snake_cased like binding sources)", *got, want)
	}

	// Port names are declared contracts: params and schema ports keep the
	// author's exact casing, exactly like capability ports today.
	if canonical.Params[0].Name != "order" || canonical.Params[1].Name != "traceId" {
		t.Errorf("params were case-normalized: %+v", canonical.Params)
	}
	if canonical.Result.Schema[0].Name != "order" || canonical.Result.Schema[1].Name != "trackingId" {
		t.Errorf("schema ports were case-normalized: %+v", canonical.Result.Schema)
	}
}
