package manifest

import (
	"github.com/neuron-runtime/neuron/application/project"
	"github.com/neuron-runtime/neuron/shared/types/core"
)

// FromResolvedProject converts a resolved YAML assembly into the canonical
// Assembly manifest. This is the bridge between the YAML frontend
// (project/) and the source-language-neutral manifest boundary.
//
// Project variables (neuron.config.*) are deliberately not applied here:
// build.BuildAssembly writes them into the manifest exactly once, for
// every authoring language, so the YAML frontend cannot diverge from the
// TypeScript frontend.
func FromResolvedProject(rp *project.ResolvedProject) *Assembly {
	if rp == nil {
		return nil
	}

	s := &Assembly{
		APIVersion: "neuron/v1",
		Kind:       "Assembly",
		Metadata: Metadata{
			Name:    rp.Assembly.Definition.Metadata.Name,
			Version: rp.Assembly.Definition.Metadata.Version,
		},
	}

	for _, rs := range rp.Assembly.Capabilities {
		s.Capabilities = append(s.Capabilities, capabilityFrom(rs))
	}

	for _, rc := range rp.Assembly.Bindings {
		s.Bindings = append(s.Bindings, bindingFrom(rc))
	}

	s.Result = assemblyResultFrom(rp.Assembly.Definition.Result)

	return s
}

// assemblyResultFrom converts the YAML-authored result contract into its
// canonical manifest shape. The YAML surface stores the shared canonical type
// directly (no second schema to drift), so only the container conversion into
// the manifest representation remains here.
func assemblyResultFrom(r *core.AssemblyResult) *AssemblyResult {
	if r == nil {
		return nil
	}

	result := &AssemblyResult{
		Schema: make([]Port, 0, len(r.Schema)),
		Fields: make([]AssemblyResultField, 0, len(r.Fields)),
	}
	for _, p := range r.Schema {
		result.Schema = append(result.Schema, Port{
			Name:     p.Name,
			Type:     string(p.Type),
			Required: p.Required,
		})
	}
	for _, f := range r.Fields {
		result.Fields = append(result.Fields, AssemblyResultField{
			Name:   f.Name,
			Source: f.Source,
		})
	}
	return result
}

func capabilityFrom(rs project.ResolvedCapability) Capability {
	spec := rs.Definition.Spec

	svc := Capability{
		Name:        rs.Definition.Metadata.Name,
		Version:     rs.Definition.Metadata.Version,
		Description: rs.Definition.Metadata.Description,
		CapabilityRuntime: CapabilityRuntimeSpec{
			Name:          spec.CapabilityRuntime.Type,
			Version:       spec.CapabilityRuntime.Version,
			Registry:      spec.CapabilityRuntime.Source,
			RuntimeConfig: runtimeConfigFrom(spec),
		},
		Config: spec.Config,
	}

	for _, m := range spec.Mappings {
		port := Port{
			Name:     m.Target,
			Type:     "any",
			Required: m.Direction == "input",
		}
		if m.Direction == "input" {
			svc.Params = append(svc.Params, port)
		} else if m.Direction == "output" {
			svc.Results = append(svc.Results, port)
		}
	}

	return svc
}

// runtimeConfigFrom folds the YAML runtime configuration into the single
// canonical location: the runtime declaration's runtimeConfig.
//
// The YAML surface still accepts a capability-level `execution:` block for
// backwards compatibility. It is authoring sugar only. It never survives into
// the canonical manifest as its own field, so there is exactly one place a
// capability's runtime configuration can be expressed.
//
// When an author declares both, the nested `capability runtime: runtimeConfig:`
// block wins, and the legacy `execution:` block fills only the values the
// author left unset.
func runtimeConfigFrom(spec project.CapabilitySpec) *core.RuntimeConfig {
	declared := spec.CapabilityRuntime.RuntimeConfig.Clone()
	if spec.Execution == nil {
		return declared
	}

	if declared == nil {
		declared = &core.RuntimeConfig{}
	}
	if declared.Execution == nil && (spec.Execution.Mode != "" || spec.Execution.Timeout != "") {
		declared.Execution = &core.RuntimeExecution{}
	}
	if declared.Execution != nil {
		if declared.Execution.Mode == "" && spec.Execution.Mode != "" {
			declared.Execution.Mode = core.RuntimeExecutionMode(spec.Execution.Mode)
		}
		if declared.Execution.Timeout == "" && spec.Execution.Timeout != "" {
			declared.Execution.Timeout = spec.Execution.Timeout
		}
	}

	// The legacy field counted *retries*, meaning invocations beyond the
	// first. The canonical field counts total attempts, so it is one higher.
	// The legacy schema had no policy selector, and the compiler it replaced
	// always assumed exponential backoff, so that is what is carried over.
	if declared.Retry == nil && spec.Execution.Retries > 0 {
		declared.Retry = &core.RuntimeRetry{
			Policy:      core.RetryPolicyExponential,
			MaxAttempts: spec.Execution.Retries + 1,
		}
	}

	return declared
}

func bindingFrom(rc project.ResolvedBinding) Binding {
	conn := Binding{
		From: rc.Definition.From,
		To:   rc.Definition.To,
	}

	for _, m := range rc.Definition.Mappings {
		conn.Mappings = append(conn.Mappings, BindingMapping{
			Target:     m.Target,
			Expression: m.Expression,
		})
	}

	for _, v := range rc.Definition.Validations {
		conn.Validations = append(conn.Validations, BindingValidation{
			Expression: v.Expression,
			Message:    v.Message,
		})
	}

	return conn
}
