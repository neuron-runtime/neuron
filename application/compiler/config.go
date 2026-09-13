package compiler

import (
	"github.com/Muhammad-Jay/neuron/application/compiler/manifest"
	shadexec "github.com/Muhammad-Jay/neuron/shared/types/executor"
)

// ExecutionConfigurations is the consolidated project/runtime configuration
// payload sent to N.O.R.E. alongside a registered system. N.O.R.E. persists
// it opaquely today but will consume it for runtime assembly (executor
// discovery, storage, inspector, runtime defaults) in the future.
//
// The structure is intentionally decoupled from both the manifest source
// languages and N.O.R.E.'s internal model, so it can evolve independently.
//
// Assembly is the CLI's responsibility (register builds the payload from the
// effective configuration, not from the manifest), so the manifest stays a
// purely source-language-neutral description of a System.
type ExecutionConfigurations struct {
	// ExecutorRegistries lists the registries that can supply executor
	// implementations for the system's services.
	ExecutorRegistries []manifest.ExecutorRegistry `json:"executor_registries,omitempty"`

	// ExecutorRequirements is an indexed view of the executors each
	// service requires, keyed by (type, version, source).
	ExecutorRequirements []manifest.ExecutorRequirement `json:"executor_requirements,omitempty"`

	// ResolvedExecutors is the frozen dependency set produced when a
	// Deployment is prepared. Authoring and registering a System only records
	// requirements; this slice pins the exact resolved version, registry and
	// checksum so N.O.R.E. can execute services without resolving or
	// installing anything.
	ResolvedExecutors []shadexec.ResolvedExecutor `json:"resolved_executors,omitempty"`

	// Runtime describes runtime execution defaults.
	Runtime manifest.RuntimeConfig `json:"runtime,omitempty"`

	// Storage describes the durable storage provider.
	Storage manifest.StorageConfig `json:"storage,omitempty"`

	// Inspector describes the inspector endpoint.
	Inspector manifest.InspectorConfig `json:"inspector,omitempty"`
}

// ExecutorRequirements groups services by their executor key so the caller can
// see which executors are needed and which services use each. It indexes the
// manifest's service executor specifications without importing runtime or
// config concerns.
func ExecutorRequirements(services []manifest.Service) []manifest.ExecutorRequirement {
	type key struct {
		Name     string
		Version  string
		Registry string
	}

	index := make(map[key]*manifest.ExecutorRequirement)

	for _, svc := range services {
		exec := svc.Executor
		k := key{exec.Name, exec.Version, exec.Registry}

		req, ok := index[k]
		if !ok {
			req = &manifest.ExecutorRequirement{
				Name:     exec.Name,
				Version:  exec.Version,
				Registry: exec.Registry,
			}
			index[k] = req
		}
		req.Services = append(req.Services, svc.Name)
	}

	result := make([]manifest.ExecutorRequirement, 0, len(index))
	for _, req := range index {
		result = append(result, *req)
	}
	return result
}
