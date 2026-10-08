package compiler

import (
	"reflect"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/application/compiler/manifest"
	"github.com/neuron-runtime/neuron/shared/types/core"
)

// resultManifest extends the shared fixture with an Assembly contract: a
// param, a declared schema, and a capability-scoped field mapping.
func resultManifest() *manifest.Assembly {
	m := testManifest()
	m.Params = []manifest.Port{{Name: "order", Type: "object", Required: true}}
	m.Result = &manifest.AssemblyResult{
		Schema: []manifest.Port{{Name: "status", Type: "string", Required: true}},
		Fields: []manifest.AssemblyResultField{
			{Name: "status", Source: &core.ValueRef{
				Kind:       core.ValueRefCapabilityResult,
				Capability: "validate",
				Path:       []string{"output"},
			}},
		},
	}
	return m
}

func TestCompileCarriesAssemblyContractIntoCore(t *testing.T) {
	sys, err := New().Compile(resultManifest())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	if len(sys.Params) != 1 || sys.Params[0].Name != "order" {
		t.Errorf("params = %+v, want [order] verbatim", sys.Params)
	}

	if sys.Result == nil {
		t.Fatal("result is nil")
	}
	if len(sys.Result.Schema) != 1 || sys.Result.Schema[0].Name != "status" {
		t.Errorf("schema = %+v, want [status]", sys.Result.Schema)
	}
	if len(sys.Result.Fields) != 1 {
		t.Fatalf("fields = %d, want 1", len(sys.Result.Fields))
	}
	field := sys.Result.Fields[0]
	want := core.AssemblyResultField{Name: "status", Source: &core.ValueRef{
		Kind:       core.ValueRefCapabilityResult,
		Capability: "validate",
		Path:       []string{"output"},
	}}
	if !reflect.DeepEqual(field, want) {
		t.Errorf("field = %+v, want %+v", field, want)
	}
}

func TestCompileAssemblyContractWithMultipleCapabilitiesAndLiteral(t *testing.T) {
	m := resultManifest()
	m.Result.Schema = []manifest.Port{
		{Name: "status", Type: "string", Required: true},
		{Name: "count", Type: "number", Required: true},
		{Name: "traceId", Type: "string"},
	}
	m.Result.Fields = []manifest.AssemblyResultField{
		{Name: "status", Source: &core.ValueRef{
			Kind:       core.ValueRefCapabilityResult,
			Capability: "validate",
			Path:       []string{"output"},
		}},
		{Name: "count", Source: &core.ValueRef{
			Kind:       core.ValueRefCapabilityResult,
			Capability: "process",
			Path:       []string{"result"},
		}},
		{Name: "traceId", Source: &core.ValueRef{
			Kind:  core.ValueRefLiteral,
			Value: "fixed-trace",
		}},
	}

	sys, err := New().Compile(m)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(sys.Result.Fields) != 3 {
		t.Fatalf("fields = %d, want 3", len(sys.Result.Fields))
	}
	if sys.Result.Fields[1].Source.Capability != "process" {
		t.Errorf("second field source = %+v, want process capabilityResult", sys.Result.Fields[1].Source)
	}
	if sys.Result.Fields[2].Source.Kind != core.ValueRefLiteral {
		t.Errorf("third field source = %+v, want literal", sys.Result.Fields[2].Source)
	}
}

func TestCompileAssemblyContractAcceptsAssemblyParamsAndVariableSources(t *testing.T) {
	m := resultManifest()
	m.Result.Schema = []manifest.Port{
		{Name: "order", Type: "object", Required: true},
		{Name: "region", Type: "string"},
	}
	m.Result.Fields = []manifest.AssemblyResultField{
		{Name: "order", Source: &core.ValueRef{
			Kind: core.ValueRefAssemblyParams,
			Path: []string{"order"},
		}},
		{Name: "region", Source: &core.ValueRef{
			Kind: core.ValueRefVariable,
			Path: []string{"region"},
		}},
	}

	sys, err := New().Compile(m)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(sys.Result.Fields) != 2 {
		t.Fatalf("fields = %d, want 2", len(sys.Result.Fields))
	}
	if sys.Result.Fields[0].Source.Kind != core.ValueRefAssemblyParams {
		t.Errorf("first field source = %+v, want assemblyParams", sys.Result.Fields[0].Source)
	}
	if sys.Result.Fields[1].Source.Kind != core.ValueRefVariable {
		t.Errorf("second field source = %+v, want variable", sys.Result.Fields[1].Source)
	}
}

func TestCompileAssemblyContractRejectsUnknownCapability(t *testing.T) {
	m := resultManifest()
	m.Result.Fields[0].Source.Capability = "missing"

	_, err := New().Compile(m)
	if err == nil || !strings.Contains(err.Error(), `references unknown capability "missing"`) {
		t.Fatalf("err = %v, want unknown capability error", err)
	}
}

func TestCompileAssemblyContractRejectsFieldNotDeclaredInSchema(t *testing.T) {
	m := resultManifest()
	m.Result.Fields[0].Name = "status"
	m.Result.Schema = []manifest.Port{{Name: "trackingId", Type: "string"}}

	_, err := New().Compile(m)
	if err == nil || !strings.Contains(err.Error(), `declares no field named "status"`) {
		t.Fatalf("err = %v, want schema membership error", err)
	}
}

func TestCompileAssemblyContractRejectsUnknownCapabilityResultPort(t *testing.T) {
	m := resultManifest()
	m.Result.Fields[0].Source.Path = []string{"missing"}

	_, err := New().Compile(m)
	if err == nil || !strings.Contains(err.Error(), `declares no result port matching "missing"`) {
		t.Fatalf("err = %v, want unknown result port error", err)
	}
}

func TestCompileAssemblyContractRejectsUnknownCapabilityParamsPort(t *testing.T) {
	m := resultManifest()
	m.Result.Fields[0].Source = &core.ValueRef{
		Kind:       core.ValueRefCapabilityParams,
		Capability: "validate",
		Path:       []string{"missing"},
	}

	_, err := New().Compile(m)
	if err == nil || !strings.Contains(err.Error(), `declares no param port matching "missing"`) {
		t.Fatalf("err = %v, want unknown param port error", err)
	}
}

func TestCompileAssemblyContractRejectsEmptySourcePath(t *testing.T) {
	m := resultManifest()
	m.Result.Fields[0].Source.Path = nil

	_, err := New().Compile(m)
	if err == nil || !strings.Contains(err.Error(), "empty path") {
		t.Fatalf("err = %v, want empty path error", err)
	}
}

func TestCompileAssemblyContractRejectsNilSource(t *testing.T) {
	m := resultManifest()
	m.Result.Fields[0].Source = nil

	_, err := New().Compile(m)
	if err == nil || !strings.Contains(err.Error(), "has no source reference") {
		t.Fatalf("err = %v, want nil source error", err)
	}
}

func TestCompileAssemblyContractRejectsUnsupportedSourceKind(t *testing.T) {
	m := resultManifest()
	m.Result.Fields[0].Source = &core.ValueRef{Kind: core.ValueRefKind("bogus")}

	_, err := New().Compile(m)
	if err == nil || !strings.Contains(err.Error(), `unknown reference kind "bogus"`) {
		t.Fatalf("err = %v, want unknown reference kind error", err)
	}
}

func TestCompileResultLessAssemblyIsUnchanged(t *testing.T) {
	sys, err := New().Compile(testManifest())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if sys.Result != nil {
		t.Errorf("result = %+v, want nil when no result is authored", sys.Result)
	}
	if len(sys.Params) != 0 {
		t.Errorf("params = %+v, want none when no params are authored", sys.Params)
	}
}
