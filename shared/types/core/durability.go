package core

import (
	"fmt"
	"strings"
)

// Execution durability modes. Retaining history and resuming unfinished work
// are separate guarantees, so the modes are deliberately distinct.
const (
	// DurabilityNone retains no execution or event history. Executions remain
	// observable in memory while they run, but nothing survives a restart.
	DurabilityNone = "none"

	// DurabilityHistory retains execution and event history for inspection.
	// Unfinished work is not resumed after a restart; it is reported as failed.
	DurabilityHistory = "history"

	// DurabilityDurable persists transitions and dispatch intents so unfinished
	// work can resume after a restart. It requires a transactional backend.
	DurabilityDurable = "durable"
)

// NormalizeDurability validates an execution durability mode and returns its
// canonical form. An empty value falls back to history, matching the default
// used by the application configuration.
func NormalizeDurability(value string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	switch mode {
	case "":
		return DurabilityHistory, nil
	case DurabilityNone, DurabilityHistory, DurabilityDurable:
		return mode, nil
	default:
		return "", fmt.Errorf(
			"invalid execution durability %q: supported values are %q, %q, and %q",
			value, DurabilityNone, DurabilityHistory, DurabilityDurable,
		)
	}
}
