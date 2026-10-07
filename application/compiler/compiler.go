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
		ID:          core.NewID("assembly_"),
		Name:        canonical.Metadata.Name,
		Description: canonical.Metadata.Description,
		Version:     canonical.Metadata.Version,
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

	return &core.Assembly{
		Metadata: sysMeta,
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
