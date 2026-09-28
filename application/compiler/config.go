package compiler

import (
	"github.com/Muhammad-Jay/neuron/application/compiler/manifest"
	capabilityrt "github.com/Muhammad-Jay/neuron/shared/types/capabilityruntime"
)

// ExecutionConfigurations is the consolidated project/runtime configuration
// payload sent to N.O.R.E. alongside a registered assembly. N.O.R.E. persists
// it opaquely today but will consume it for runtime assembly (capability runtime
// discovery, storage, inspector, runtime defaults) in the future.
//
// The structure is intentionally decoupled from both the manifest source
// languages and N.O.R.E.'s internal model, so it can evolve independently.
//
// Assembly is the CLI's responsibility (register builds the payload from the
// effective configuration, not from the manifest), so the manifest stays a
// purely source-language-neutral description of a Assembly.
type ExecutionConfigurations struct {
	// CapabilityRuntimeRegistries lists the registries that can supply capability runtime
	// implementations for the assembly's capabilities.
	CapabilityRuntimeRegistries []manifest.CapabilityRuntimeRegistry `json:"capabilityRuntime_registries,omitempty"`

	// CapabilityRuntimeRequirements is an indexed view of the capability runtimes each
	// capability requires, keyed by (type, version, source).
	CapabilityRuntimeRequirements []manifest.CapabilityRuntimeRequirement `json:"capabilityRuntime_requirements,omitempty"`

	// ResolvedCapabilityRuntimes is the frozen dependency set produced when a
	// Deployment is prepared. Authoring and registering a Assembly only records
	// requirements; this slice pins the exact resolved version, registry and
	// checksum so N.O.R.E. can execute capabilities without resolving or
	// installing anything.
	ResolvedCapabilityRuntimes []capabilityrt.ResolvedCapabilityRuntime `json:"resolved_capabilityRuntimes,omitempty"`

	// Runtime describes runtime execution defaults.
	Runtime manifest.RuntimeConfig `json:"runtime,omitempty"`

	// Storage describes the durable storage provider.
	Storage manifest.StorageConfig `json:"storage,omitempty"`

	// Inspector describes the inspector endpoint.
	Inspector manifest.InspectorConfig `json:"inspector,omitempty"`
}

// CapabilityRuntimeRequirements groups capabilities by their capability runtime key so the caller can
// see which capability runtimes are needed and which capabilities use each. It indexes the
// manifest's capability capability runtime specifications without importing runtime or
// config concerns.
func CapabilityRuntimeRequirements(capabilities []manifest.Capability) []manifest.CapabilityRuntimeRequirement {
	type key struct {
		Name     string
		Version  string
		Registry string
	}

	index := make(map[key]*manifest.CapabilityRuntimeRequirement)

	for _, svc := range capabilities {
		exec := svc.CapabilityRuntime
		k := key{exec.Name, exec.Version, exec.Registry}

		req, ok := index[k]
		if !ok {
			req = &manifest.CapabilityRuntimeRequirement{
				Name:     exec.Name,
				Version:  exec.Version,
				Registry: exec.Registry,
			}
			index[k] = req
		}
		req.Capabilities = append(req.Capabilities, svc.Name)
	}

	result := make([]manifest.CapabilityRuntimeRequirement, 0, len(index))
	for _, req := range index {
		result = append(result, *req)
	}
	return result
}
