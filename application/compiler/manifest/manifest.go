package manifest

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
	Name              string                `json:"name"`
	Version           string                `json:"version,omitempty"`
	Description       string                `json:"description,omitempty"`
	CapabilityRuntime CapabilityRuntimeSpec `json:"capabilityRuntime"`
	Params            []Port                `json:"params"`
	Results           []Port                `json:"results"`
	Config            map[string]any        `json:"config,omitempty"`
	Execution         *ExecutionConfig      `json:"execution,omitempty"`
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
	Name     string `json:"name"`
	Version  string `json:"version"`
	Registry string `json:"registry"`
}

// ExecutionConfig contains capability-level execution behavior.
type ExecutionConfig struct {
	Mode           string `json:"mode,omitempty"`
	Timeout        string `json:"timeout,omitempty"`
	Retries        int    `json:"retries,omitempty"`
	Concurrency    int    `json:"concurrency,omitempty"`
	ContinueOnFail bool   `json:"continueOnFail,omitempty"`
}

// Binding describes a directed edge between two capabilities.
type Binding struct {
	From        string              `json:"from"`
	To          string              `json:"to"`
	Mappings    []BindingMapping    `json:"mappings"`
	Validations []BindingValidation `json:"validations"`
}

// BindingMapping maps a source expression to a target path.
type BindingMapping struct {
	Target     string `json:"target"`
	Expression string `json:"expression"`
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
