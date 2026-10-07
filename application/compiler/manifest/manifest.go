package manifest

import "github.com/neuron-runtime/neuron/shared/types/core"

// Assembly is the canonical, source-language-neutral representation of a
// Neuron assembly definition. Every authoring syntax (YAML, TypeScript,
// JSON, future languages) compiles down to this structure, which is then
// persisted to .neuron/manifest.json and consumed by the compiler to
// produce a core.Assembly.
type Assembly struct {
	APIVersion   string       `json:"apiVersion"`
	Kind         string       `json:"kind"`
	Metadata     Metadata     `json:"metadata"`
	Capabilities []Capability `json:"capabilities"`
	Bindings     []Binding    `json:"bindings"`

	// Variables are project-level values supplied by the project
	// configuration. They have no meaning to the runtime itself.
	Variables map[string]any `json:"variables,omitempty"`
}

// Metadata identifies a Assembly in the manifest.
type Metadata struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

// Capability describes one unit of computation.
type Capability struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`

	// CapabilityRuntime is the runtime declaration for this capability: which
	// runtime executes it, where that runtime comes from, and how N.O.R.E.
	// should drive it.
	CapabilityRuntime CapabilityRuntimeSpec `json:"capabilityRuntime"`

	Params  []Port         `json:"params"`
	Results []Port         `json:"results"`
	Config  map[string]any `json:"config,omitempty"`
}

// Port is a typed parameter or result slot.
type Port struct {
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	Required bool           `json:"required"`
	Rules    map[string]any `json:"rules,omitempty"`
}

// CapabilityRuntimeSpec identifies the runtime capability runtime required by a capability.
type CapabilityRuntimeSpec struct {
	// Name is the logical capability runtime identity (e.g. "example:echo").
	Name string `json:"name"`

	// Version is the version requirement to resolve against the registry.
	Version string `json:"version"`

	// Registry names the registry or distribution source to obtain the runtime from.
	Registry string `json:"registry"`

	// RuntimeConfig instructs N.O.R.E. how to execute this capability through
	// this runtime. It is scoped to this capability's runtime invocation: two
	// capabilities may declare the same runtime with different runtimeConfigs,
	// while the runtime artifact itself is still resolved and installed once.
	//
	// RuntimeConfig is never capability input and is never passed to the
	// capability runtime as params. A nil value means the author declared
	// nothing and N.O.R.E. supplies every default.
	RuntimeConfig *core.RuntimeConfig `json:"runtimeConfig,omitempty"`
}

// Binding describes a directed edge between two capabilities.
type Binding struct {
	From        string              `json:"from"`
	To          string              `json:"to"`
	Mappings    []BindingMapping    `json:"mappings"`
	Validations []BindingValidation `json:"validations"`
}

// BindingMapping maps a value from the source Capability or execution context
// into a target Capability param path.
//
// Source is the canonical structured reference. Authoring surfaces that still
// emit the legacy mapping-expression dialect populate Expression instead;
// Canonicalize parses it into Source once, so the persisted canonical manifest
// only ever carries the structured form.
type BindingMapping struct {
	Target     string         `json:"target"`
	Source     *core.ValueRef `json:"source,omitempty"`
	Expression string         `json:"expression,omitempty"`
}

// BindingValidation asserts a transition condition.
type BindingValidation struct {
	Expression string `json:"expression"`
	Message    string `json:"message"`
}

// CapabilityRuntimeRegistry locates an capability runtime implementation.
type CapabilityRuntimeRegistry struct {
	Name string `json:"name,omitempty"`
	URL  string `json:"url"`
}

// CapabilityRuntimeRequirement is an indexed capability runtime dependency: a unique
// capability runtime (by name/version/registry) and the capabilities that require it.
type CapabilityRuntimeRequirement struct {
	Name         string   `json:"name"`
	Version      string   `json:"version,omitempty"`
	Registry     string   `json:"registry,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// RuntimeConfig controls runtime execution defaults.
type RuntimeConfig struct {
	Execution RuntimeExecutionConfig `json:"execution,omitempty"`
	Workers   WorkerConfig           `json:"workers,omitempty"`
}

type RuntimeExecutionConfig struct {
	Mode    string `json:"mode,omitempty"`
	Timeout string `json:"timeout,omitempty"`
}

type WorkerConfig struct {
	Min int `json:"min,omitempty"`
	Max int `json:"max,omitempty"`
}

// StorageConfig controls the durable storage provider.
type StorageConfig struct {
	Provider  string `json:"provider,omitempty"`
	Directory string `json:"directory,omitempty"`
}

// InspectorConfig controls the inspector endpoint.
type InspectorConfig struct {
	Enabled bool   `json:"enabled,omitempty"`
	Address string `json:"address,omitempty"`
}
