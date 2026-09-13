package manifest

// System is the canonical, source-language-neutral representation of a
// Neuron system definition. Every authoring syntax (YAML, TypeScript,
// JSON, future languages) compiles down to this structure, which is then
// persisted to .neuron/manifest.json and consumed by the compiler to
// produce a core.System.
type System struct {
	APIVersion string      `json:"apiVersion"`
	Kind       string      `json:"kind"`
	Metadata   Metadata    `json:"metadata"`
	Inputs     []Port      `json:"inputs,omitempty"`
	Services   []Service   `json:"services"`
	Connectors []Connector `json:"connectors"`
	Definition SystemNode  `json:"definition"`

	// Variables are project-level values supplied by the project
	// configuration. They have no meaning to the runtime itself.
	Variables map[string]any `json:"variables,omitempty"`
}

// Metadata identifies a System in the manifest.
type Metadata struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

// Service describes one unit of computation.
type Service struct {
	Name        string           `json:"name"`
	Version     string           `json:"version,omitempty"`
	Description string           `json:"description,omitempty"`
	Executor    ExecutorSpec     `json:"executor"`
	Inputs      []Port           `json:"inputs"`
	Outputs     []Port           `json:"outputs"`
	Config      map[string]any   `json:"config,omitempty"`
	Execution   *ExecutionConfig `json:"execution,omitempty"`
}

// Port is a typed input or output slot.
type Port struct {
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	Required bool           `json:"required"`
	Rules    map[string]any `json:"rules,omitempty"`
}

// ExecutorSpec identifies the runtime executor required by a service.
type ExecutorSpec struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Registry string `json:"registry"`
}

// ExecutionConfig contains service-level execution behavior.
type ExecutionConfig struct {
	Mode           string `json:"mode,omitempty"`
	Timeout        string `json:"timeout,omitempty"`
	Retries        int    `json:"retries,omitempty"`
	Concurrency    int    `json:"concurrency,omitempty"`
	ContinueOnFail bool   `json:"continueOnFail,omitempty"`
}

// Connector describes a directed edge between two services.
type Connector struct {
	From        string                `json:"from"`
	To          string                `json:"to"`
	Mappings    []ConnectorMapping    `json:"mappings"`
	Validations []ConnectorValidation `json:"validations"`
}

// ConnectorMapping maps a source expression to a target path.
type ConnectorMapping struct {
	Target     string `json:"target"`
	Expression string `json:"expression"`
}

// ConnectorValidation asserts a transition condition.
type ConnectorValidation struct {
	Expression string `json:"expression"`
	Message    string `json:"message"`
}

// SystemNode is the recursive composition AST describing how
// services are sequenced or run in parallel.
type SystemNode struct {
	Kind     string       `json:"kind"`
	Service  string       `json:"service,omitempty"`
	Steps    []SystemNode `json:"steps,omitempty"`
	Branches []SystemNode `json:"branches,omitempty"`
}

// ExecutorRegistry locates an executor implementation.
type ExecutorRegistry struct {
	Name string `json:"name,omitempty"`
	URL  string `json:"url"`
}

// ExecutorRequirement is an indexed executor dependency: a unique
// executor (by name/version/registry) and the services that require it.
type ExecutorRequirement struct {
	Name     string   `json:"name"`
	Version  string   `json:"version,omitempty"`
	Registry string   `json:"registry,omitempty"`
	Services []string `json:"services,omitempty"`
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
