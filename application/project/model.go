package project

import "time"

// ResolvedProject is the result of resolving a neuron project.
//
// It contains no unresolved entry references.
//
// It is still independent from N.O.R.E. core.System.
//
// The next layer can transform this into:
//
// ResolvedProject
//
//	↓
//
// SystemParser
//
//	↓
//
// core.System
type ResolvedProject struct {
	FormatVersion string `json:"formatVersion"`

	ResolvedAt time.Time `json:"resolvedAt"`

	System ResolvedSystem `json:"systems"`

	// ExecutorRequirements is an indexed view of the executors
	// required by all services in this project.
	ExecutorRequirements []ExecutorRequirement `json:"executorRequirements"`

	// SourceFiles records every source YAML file participating in
	// this resolved project.
	//
	// Paths are relative to the project root.
	SourceFiles []ResolvedSourceFile `json:"sourceFiles"`
}

// ResolvedSystem is a System with all referenced services resolved.
type ResolvedSystem struct {
	Definition SystemFile `json:"definition"`

	Services   []ResolvedService   `json:"services"`
	Connectors []ResolvedConnector `json:"connectors"`
}

// ResolvedConnector contains the original connector definition plus
// resolution metadata.
type ResolvedConnector struct {
	Ref        string        `json:"ref"`
	SourcePath string        `json:"sourcePath"`
	Definition ConnectorFile `json:"definition"`
}

// ResolvedService contains the original service definition plus
// resolution metadata.
type ResolvedService struct {
	Ref string `json:"ref"`

	SourcePath string `json:"sourcePath"`

	Definition ServiceFile `json:"definition"`
}

// ExecutorRequirement is an indexed executor dependency.
//
// This is useful later for:
//
//	executor install
//	executor resolve
//	executor registry
//	container preparation
//	remote executor discovery
type ExecutorRequirement struct {
	Type    string `json:"type"`
	Version string `json:"version,omitempty"`
	Source  string `json:"source,omitempty"`

	Services []string `json:"services"`
}

// ResolvedSourceFile identifies a source YAML file.
type ResolvedSourceFile struct {
	Path string `json:"path"`

	Kind string `json:"kind"`

	SHA256 string `json:"sha256"`
}

func collectExecutorRequirements(
	services []ResolvedService,
) []ExecutorRequirement {

	type key struct {
		Type    string
		Version string
		Source  string
	}

	index := make(map[key]*ExecutorRequirement)

	for _, service := range services {

		executor := service.Definition.Spec.Executor

		k := key{
			Type:    executor.Type,
			Version: executor.Version,
			Source:  executor.Source,
		}

		requirement, exists := index[k]

		if !exists {
			requirement = &ExecutorRequirement{
				Type:    executor.Type,
				Version: executor.Version,
				Source:  executor.Source,
			}

			index[k] = requirement
		}

		requirement.Services = append(
			requirement.Services,
			service.Ref,
		)
	}

	result := make(
		[]ExecutorRequirement,
		0,
		len(index),
	)

	for _, requirement := range index {
		result = append(result, *requirement)
	}

	return result
}
