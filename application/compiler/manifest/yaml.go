package manifest

import (
	"github.com/neuron-runtime/neuron/application/project"
)

// FromResolvedProject converts a resolved YAML assembly into the canonical
// Assembly manifest. variables come from the project configuration, not from
// the YAML project file itself. This is the bridge between the YAML frontend
// (project/) and the source-language-neutral manifest boundary.
func FromResolvedProject(rp *project.ResolvedProject, variables map[string]any) *Assembly {
	if rp == nil {
		return nil
	}

	s := &Assembly{
		APIVersion: "neuron/v1",
		Kind:       "Assembly",
		Metadata: Metadata{
			Name:        rp.Assembly.Definition.Metadata.Name,
			Version:     rp.Assembly.Definition.Metadata.Version,
			Description: rp.Assembly.Definition.Metadata.Description,
		},
		Variables: variables,
	}

	for _, rs := range rp.Assembly.Capabilities {
		s.Capabilities = append(s.Capabilities, capabilityFrom(rs))
	}

	for _, rc := range rp.Assembly.Bindings {
		s.Bindings = append(s.Bindings, bindingFrom(rc))
	}

	return s
}

func capabilityFrom(rs project.ResolvedCapability) Capability {
	spec := rs.Definition.Spec

	svc := Capability{
		Name:        rs.Definition.Metadata.Name,
		Version:     rs.Definition.Metadata.Version,
		Description: rs.Definition.Metadata.Description,
		CapabilityRuntime: CapabilityRuntimeSpec{
			Name:     spec.CapabilityRuntime.Type,
			Version:  spec.CapabilityRuntime.Version,
			Registry: spec.CapabilityRuntime.Source,
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

	if spec.Execution != nil {
		svc.Execution = &ExecutionConfig{
			Mode:           spec.Execution.Mode,
			Timeout:        spec.Execution.Timeout,
			Retries:        spec.Execution.Retries,
			Concurrency:    spec.Execution.Concurrency,
			ContinueOnFail: spec.Execution.ContinueOnFail,
		}
	}

	return svc
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
