package instance

import core "github.com/neuron-runtime/neuron/shared/types/core"

// Execution durability modes. Retaining history and resuming unfinished work
// are separate guarantees, so the modes are deliberately distinct. They alias
// the canonical definitions in the shared module.
const (
	// DurabilityNone retains no execution or event history. Executions remain
	// observable in memory while they run, but nothing survives a restart.
	DurabilityNone = core.DurabilityNone

	// DurabilityHistory retains execution and event history for inspection.
	// Unfinished work is not resumed after a restart; it is reported as failed.
	DurabilityHistory = core.DurabilityHistory

	// DurabilityDurable persists transitions and dispatch intents so unfinished
	// work can resume after a restart. It requires a transactional backend.
	DurabilityDurable = core.DurabilityDurable
)

// NormalizeDurability validates an execution durability mode and returns its
// canonical form. An empty value falls back to history, matching the default
// used by the application configuration.
func NormalizeDurability(value string) (string, error) {
	return core.NormalizeDurability(value)
}
