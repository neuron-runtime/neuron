package compiler

import (
	"fmt"

	"github.com/neuron-runtime/neuron/application/compiler/manifest"
	"github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// Compiler transforms a canonical Assembly manifest into the runtime
// core.Assembly representation consumed by N.O.R.E. It is source-language
// agnostic: YAML, TypeScript, JSON, or any future frontend all converge
// on manifest.Assembly before reaching this stage.
type Compiler struct{}

// New returns a Compiler.
func New() *Compiler {
	return &Compiler{}
}

// Compile converts a manifest into core.Assembly.
func (c *Compiler) Compile(m *manifest.Assembly) (*core.Assembly, error) {
	if m == nil {
		return nil, fmt.Errorf("manifest is nil")
	}

	// Binding mapping sources are authored per-surface and canonicalized when
	// the manifest is written. Re-canonicalizing here keeps the compiler safe
	// against manifests that bypass the authoring pipeline (hand-edited or
	// produced by a future surface) without requiring every caller to
	// remember the step.
	canonical, err := manifest.Canonicalize(m)
	if err != nil {
		return nil, err
	}

	sysMeta := core.Metadata{
		ID:      core.NewID("assembly_"),
		Name:    canonical.Metadata.Name,
		Version: canonical.Metadata.Version,
	}

	capabilities := make([]core.Capability, 0, len(canonical.Capabilities))
	capabilityMap := make(map[string]core.Capability)

	for _, rs := range canonical.Capabilities {
		svc, err := convertCapability(rs)
		if err != nil {
			return nil, err
		}
		capabilityMap[rs.Name] = svc
		capabilities = append(capabilities, svc)
	}

	bindings := make([]core.Binding, 0, len(canonical.Bindings))
	for _, rc := range canonical.Bindings {
		conn, err := convertBinding(rc, capabilityMap)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, conn)
	}

	// The Assembly's params and result are part of its public contract, so
	// they are validated and carried into core before the assembly is hashed.
	params := convertPorts(canonical.Params)
	result, err := compileAssemblyResult(canonical, capabilityMap)
	if err != nil {
		return nil, err
	}

	return &core.Assembly{
		Metadata: sysMeta,
		Params:   params,
		Result:   result,
		Specification: core.AssemblySpec{
			Capabilities: capabilities,
			Triggers:     nil,
			Bindings:     bindings,
		},
	}, nil
}

// InstanceKey computes the protocol.InstanceKey for a manifest.
// The key identity is (assemblyID, version, hash, env). The hash is
// derived from the compiled core.Assembly. env is the execution
// environment (e.g. "development", "detach") supplied by the caller
// from the effective configuration.
func (c *Compiler) InstanceKey(m *manifest.Assembly, env string) (protocol.InstanceKey, error) {
	sys, err := c.Compile(m)
	if err != nil || sys == nil {
		return protocol.InstanceKey{}, fmt.Errorf("compile manifest for instance key: %w", err)
	}

	hash, err := protocol.HashAssembly(*sys)
	if err != nil {
		return protocol.InstanceKey{}, fmt.Errorf("hash assembly: %w", err)
	}

	if env == "" {
		env = "development"
	}

	return protocol.InstanceKey{
		AssemblyID: m.Metadata.Name,
		Version:    m.Metadata.Version,
		Hash:       hash,
		Env:        env,
	}, nil
}

// convertPorts copies authored ports into the runtime representation. Port
// names keep the author's exact casing, exactly as capability param/result
// ports do today: only identifier tokens that participate in expression
// resolution are ever snake_cased, and a declared contract is not one.
func convertPorts(ports []manifest.Port) []core.Port {
	out := make([]core.Port, 0, len(ports))
	for _, p := range ports {
		out = append(out, core.Port{
			Name:     p.Name,
			Type:     core.ValueType(p.Type),
			Required: p.Required,
		})
	}
	return out
}

// compileAssemblyResult validates the Assembly's declared result contract
// against the manifest and carries it into the runtime representation.
//
// Every field's name must be declared by the schema (the field name is the
// public contract and is matched exactly), every field must carry a
// validated structured source, and a capability-scoped source must name an
// existing capability and one of its declared ports. Validation lives here,
// in the compiler, so a bad result mapping fails the build for every
// authoring language — not at the CLI, and not inside N.O.R.E.
func compileAssemblyResult(m *manifest.Assembly, capabilityMap map[string]core.Capability) (*core.AssemblyResult, error) {
	if m.Result == nil {
		return nil, nil
	}

	result := &core.AssemblyResult{
		Schema: convertPorts(m.Result.Schema),
	}
	schemaNames := make(map[string]bool, len(result.Schema))
	for _, port := range result.Schema {
		schemaNames[port.Name] = true
	}

	for _, field := range m.Result.Fields {
		prefix := fmt.Sprintf("assembly result field %q", field.Name)

		if !schemaNames[field.Name] {
			return nil, fmt.Errorf("%s: the assembly result schema declares no field named %q", prefix, field.Name)
		}
		if field.Source == nil {
			return nil, fmt.Errorf("%s: has no source reference", prefix)
		}
		if err := field.Source.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", prefix, err)
		}

		switch field.Source.Kind {
		case core.ValueRefCapabilityResult, core.ValueRefCapabilityParams:
			capability, ok := capabilityMap[field.Source.Capability]
			if !ok {
				return nil, fmt.Errorf("%s: references unknown capability %q", prefix, field.Source.Capability)
			}
			if len(field.Source.Path) == 0 {
				return nil, fmt.Errorf("%s: %s reference to capability %q has an empty path; it must name one declared port", prefix, field.Source.Kind, field.Source.Capability)
			}

			ports := capability.Results
			portKind := "result"
			if field.Source.Kind == core.ValueRefCapabilityParams {
				ports = capability.Params
				portKind = "param"
			}
			root := core.CamelToSnake(field.Source.Path[0])
			if !hasPort(ports, root) {
				return nil, fmt.Errorf("%s: capability %q declares no %s port matching %q (snake_case: %q)", prefix, field.Source.Capability, portKind, field.Source.Path[0], root)
			}
		}

		result.Fields = append(result.Fields, core.AssemblyResultField{
			Name:   field.Name,
			Source: field.Source,
		})
	}

	return result, nil
}

func hasPort(ports []core.Port, name string) bool {
	for _, port := range ports {
		if core.CamelToSnake(port.Name) == name {
			return true
		}
	}
	return false
}

func convertCapability(s manifest.Capability) (core.Capability, error) {
	var params, results []core.Port
	for _, p := range s.Params {
		params = append(params, core.Port{
			Name:     p.Name,
			Type:     core.ValueType(p.Type),
			Required: p.Required,
		})
	}
	for _, p := range s.Results {
		results = append(results, core.Port{
			Name:     p.Name,
			Type:     core.ValueType(p.Type),
			Required: p.Required,
		})
	}

	// The runtime configuration is authored per capability runtime
	// invocation. The compiler validates what was declared and carries it
	// through unchanged; it never substitutes a default, because deciding what
	// an omission means belongs to the runtime engine, not to compilation.
	if err := s.CapabilityRuntime.RuntimeConfig.Validate(); err != nil {
		return core.Capability{}, fmt.Errorf("capability %s: %w", s.Name, err)
	}

	return core.Capability{
		Metadata: core.Metadata{
			ID:          core.ID(s.Name),
			Name:        s.Name,
			Description: s.Description,
			Version:     s.Version,
		},
		Type:                     core.CapabilityRuntimeType(s.CapabilityRuntime.Name),
		CapabilityConfigurations: s.Config,
		RuntimeConfig:            s.CapabilityRuntime.RuntimeConfig.Clone(),
		Params:                   params,
		Results:                  results,
	}, nil
}

func convertBinding(conn manifest.Binding, capabilityMap map[string]core.Capability) (core.Binding, error) {
	fromSvc, ok := capabilityMap[conn.From]
	if !ok {
		return core.Binding{}, fmt.Errorf("binding from %q to %q: from capability %q not found", conn.From, conn.To, conn.From)
	}
	toSvc, ok := capabilityMap[conn.To]
	if !ok {
		return core.Binding{}, fmt.Errorf("binding from %q to %q: to capability %q not found", conn.From, conn.To, conn.To)
	}

	var mappings []core.MappingRule
	for _, m := range conn.Mappings {
		if m.Source == nil {
			return core.Binding{}, fmt.Errorf("binding %s -> %s: mapping target %q has no source", conn.From, conn.To, m.Target)
		}
		source := *m.Source
		if ref := source; (ref.Kind == core.ValueRefCapabilityResult || ref.Kind == core.ValueRefCapabilityParams) && ref.Capability != conn.From {
			return core.Binding{}, fmt.Errorf("binding %s -> %s: mapping target %q references capability %q but the binding originates at %q", conn.From, conn.To, m.Target, ref.Capability, conn.From)
		}
		mappings = append(mappings, core.MappingRule{
			TargetPath: m.Target,
			Source:     &source,
		})
	}

	var validations []core.ValidationRule
	for _, v := range conn.Validations {
		validations = append(validations, core.ValidationRule{
			Expression: v.Expression,
			Message:    v.Message,
		})
	}

	return core.Binding{
		Metadata: core.Metadata{
			ID:   core.NewID("binding_"),
			Name: conn.From + "->" + conn.To,
		},
		From: core.Endpoint{
			CapabilityID: fromSvc.Metadata.ID,
		},
		To: core.Endpoint{
			CapabilityID: toSvc.Metadata.ID,
		},
		Mappings:    mappings,
		Validations: validations,
	}, nil
}
