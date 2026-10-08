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

	// Params declare the Assembly's public invocation contract. They are the
	// source-language-neutral shape of the TypeScript paramsSchema.
	Params []Port `json:"params,omitempty"`

	// Result declares the Assembly's public result contract: the schema a
	// caller reads after a synchronous execution completes, and the structured
	// mapping that produces each field.
	Result *AssemblyResult `json:"result,omitempty"`

	// Variables are project-level values supplied by the project
	// configuration. They have no meaning to the runtime itself.
	Variables map[string]any `json:"variables,omitempty"`
}

// AssemblyResult is the public result contract of an Assembly execution: the
// schema a caller reads after a synchronous run completes, plus the structured
// mapping from capability results (and assembly params, project variables, and
// literals) to each declared field. Field order is the author's declared
// contract and is preserved verbatim: nothing here is an arbitrary expression
// string, and the runtime resolves every field from its structured source.
type AssemblyResult struct {
	Schema []Port                `json:"schema"`
	Fields []AssemblyResultField `json:"fields"`
}

// AssemblyResultField maps one declared Assembly result field to the value
// that must produce it at execution time.
type AssemblyResultField struct {
	// Name is the public field name, exactly as the author declared it; it is
	// the contract callers read, so it is matched exactly and never rewritten.
	Name string `json:"name"`

	// Source is the structured reference the runtime resolves at execution.
	Source *core.ValueRef `json:"source"`
}

// Metadata identifies a Assembly in the manifest.
//
// Name and Version are the identity; both are owned by neuron.config.* and
// injected by ApplyIdentity. Description is deliberately not part of the
// model: identity is configuration-owned only.
type Metadata struct {
	Name    string `json:"name"`
	Version string `json:"version"`
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
