package utils

import (
	"fmt"
	"strings"

	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
)

// NormalizeInstanceTarget maps a user-facing target to the canonical string
// for the REST API: instance IDs (inst_*) pass through unchanged; anything
// else is parsed as a system key and converted to its colon-encoded form.
func NormalizeInstanceTarget(target string) (string, error) {

	target = strings.TrimSpace(target)
	if target == "" {
		return "", nil
	}
	if strings.HasPrefix(target, "inst_") {
		return target, nil
	}
	key, err := protocol.ParseUserKey(target)
	if err != nil {
		return "", fmt.Errorf("invalid instance target %q: %w", target, err)
	}
	return key.ColonString(), nil
}
