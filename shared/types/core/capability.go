package core

// CapabilityConfigurations contains capability-specific configuration
// authored by a user. Values may include template strings such as:
// "Write to {{ params.customer.name }}".
//
// These values are user input to the capability. Built-in runtimes may merge
// them into the capability's params. They are deliberately distinct from
// RuntimeConfig, which instructs N.O.R.E. instead of the capability.
type CapabilityConfigurations map[string]any

type Capability struct {
	Metadata Metadata
	Type     CapabilityRuntimeType

	// CapabilityConfigurations is user-authored configuration data handed to
	// the capability as input.
	CapabilityConfigurations CapabilityConfigurations

	// RuntimeConfig instructs N.O.R.E. how to execute this capability through
	// its declared Capability Runtime. It is never passed to the capability as
	// input. A nil value means the author declared nothing and N.O.R.E. applies
	// its own defaults.
	RuntimeConfig *RuntimeConfig

	Params  []Port
	Results []Port
}

// Trigger is an executable Capability that starts an Assembly execution.
type Trigger struct {
	Capability
}
