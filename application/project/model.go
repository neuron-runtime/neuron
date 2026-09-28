package project

import "time"

// ResolvedProject is the result of resolving a neuron project.
//
// It contains no unresolved entry references.
//
// It is still independent from N.O.R.E. core.Assembly.
//
// The next layer can transform this into:
//
// ResolvedProject
//
//	↓
//
// AssemblyParser
//
//	↓
//
// core.Assembly
type ResolvedProject struct {
	FormatVersion string `json:"formatVersion"`

	ResolvedAt time.Time `json:"resolvedAt"`

	Assembly ResolvedAssembly `json:"assemblies"`

	// CapabilityRuntimeRequirements is an indexed view of the capability runtimes
	// required by all capabilities in this project.
	CapabilityRuntimeRequirements []CapabilityRuntimeRequirement `json:"capabilityRuntimeRequirements"`

	// SourceFiles records every source YAML file participating in
	// this resolved project.
	//
	// Paths are relative to the project root.
	SourceFiles []ResolvedSourceFile `json:"sourceFiles"`
}

// ResolvedAssembly is a Assembly with all referenced capabilities resolved.
type ResolvedAssembly struct {
	Definition AssemblyFile `json:"definition"`

	Capabilities []ResolvedCapability `json:"capabilities"`
	Bindings     []ResolvedBinding    `json:"bindings"`
}

// ResolvedBinding contains the original binding definition plus
// resolution metadata.
type ResolvedBinding struct {
	Ref        string      `json:"ref"`
	SourcePath string      `json:"sourcePath"`
	Definition BindingFile `json:"definition"`
}

// ResolvedCapability contains the original capability definition plus
// resolution metadata.
type ResolvedCapability struct {
	Ref string `json:"ref"`

	SourcePath string `json:"sourcePath"`

	Definition CapabilityFile `json:"definition"`
}

// CapabilityRuntimeRequirement is an indexed capability runtime dependency.
//
// This is useful later for:
//
//	capability runtime install
//	capability runtime resolve
//	capability runtime registry
//	container preparation
//	remote capability runtime discovery
type CapabilityRuntimeRequirement struct {
	Type    string `json:"type"`
	Version string `json:"version,omitempty"`
	Source  string `json:"source,omitempty"`

	Capabilities []string `json:"capabilities"`
}

// ResolvedSourceFile identifies a source YAML file.
type ResolvedSourceFile struct {
	Path string `json:"path"`

	Kind string `json:"kind"`

	SHA256 string `json:"sha256"`
}

func collectCapabilityRuntimeRequirements(
	capabilities []ResolvedCapability,
) []CapabilityRuntimeRequirement {

	type key struct {
		Type    string
		Version string
		Source  string
	}

	index := make(map[key]*CapabilityRuntimeRequirement)

	for _, capability := range capabilities {

		capabilityRuntime := capability.Definition.Spec.CapabilityRuntime

		k := key{
			Type:    capabilityRuntime.Type,
			Version: capabilityRuntime.Version,
			Source:  capabilityRuntime.Source,
		}

		requirement, exists := index[k]

		if !exists {
			requirement = &CapabilityRuntimeRequirement{
				Type:    capabilityRuntime.Type,
				Version: capabilityRuntime.Version,
				Source:  capabilityRuntime.Source,
			}

			index[k] = requirement
		}

		requirement.Capabilities = append(
			requirement.Capabilities,
			capability.Ref,
		)
	}

	result := make(
		[]CapabilityRuntimeRequirement,
		0,
		len(index),
	)

	for _, requirement := range index {
		result = append(result, *requirement)
	}

	return result
}
