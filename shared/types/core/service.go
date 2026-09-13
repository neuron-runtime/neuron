package core

// ServiceConfigurations contains Service-specific configuration authored by a user.
// Values may include template strings such as:
// "Write to {{ input.customer.name }}".
type ServiceConfigurations map[string]any

// RuntimeConfigurations controls how N.O.R.E executes the Service.
// These fields are intentionally minimal; the runtime does not apply them yet.
type RuntimeConfigurations struct {
	Timeout string
	Retry   RetryPolicy
}

type RetryPolicy struct {
	MaxAttempts int
	Backoff     string
}

type Service struct {
	Metadata Metadata
	Type     ExecutorType

	ServiceConfigurations ServiceConfigurations
	RuntimeConfigurations RuntimeConfigurations

	Inputs  []Port
	Outputs []Port
}

// Trigger is an executable Service that starts a System execution.
type Trigger struct {
	Service
}
