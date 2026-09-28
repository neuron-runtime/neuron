package core

// CapabilityConfigurations contains capability-specific configuration
// authored by a user. Values may include template strings such as:
// "Write to {{ params.customer.name }}".
type CapabilityConfigurations map[string]any

// RuntimeConfigurations controls how N.O.R.E executes the Capability.
// These fields are intentionally minimal; the runtime does not apply them yet.
type RuntimeConfigurations struct {
	Timeout string
	Retry   RetryPolicy
}

type RetryPolicy struct {
	MaxAttempts int
	Backoff     string
}

type Capability struct {
	Metadata Metadata
	Type     CapabilityRuntimeType

	CapabilityConfigurations CapabilityConfigurations
	RuntimeConfigurations    RuntimeConfigurations

	Params  []Port
	Results []Port
}

// Trigger is an executable Capability that starts an Assembly execution.
type Trigger struct {
	Capability
}
