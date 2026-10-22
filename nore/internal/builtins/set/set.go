// Package set implements the neuron:core:set capability runtime.
package set

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/neuron-runtime/neuron/nore/internal/builtins/input"
	"github.com/neuron-runtime/neuron/nore/internal/contracts"
)

// Set materializes a capability's value set.
//
// It combines the capability's own configuration with the params bound to the
// invocation and returns the combination. It is the default runtime for a
// capability that declares none, which makes it the way an assembly supplies a
// value set to a downstream capability without writing a capability runtime.
//
// The merge follows the shared rule: params override configuration. A param is
// an explicit argument at the call site, and a configuration value is the
// ambient default declared alongside the capability, so the explicit value
// wins.
type Set struct{}

// Execute returns the merged value set.
//
// The result carries the names of the merged set, so a capability that
// declares a result schema declares exactly those names.
func (Set) Execute(ctx context.Context, execution contracts.ExecutionContext) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	values := input.New("neuron:core:set", execution.CapabilityConfigurations, execution.Params)

	format, err := values.Format("format", input.FormatJSON)
	if err != nil {
		return nil, err
	}

	merged := values.All()
	// The format is an instruction to the built-in rather than a value of the
	// set, so it is not part of what the set returns.
	delete(merged, "format")

	if format == input.FormatText {
		return renderText(merged), nil
	}
	return merged, nil
}

// renderText represents every value of the set as text.
//
// The names of the set are kept, so the result of the capability has the same
// shape in both formats: only the representation of the values changes.
func renderText(values map[string]any) map[string]any {
	rendered := make(map[string]any, len(values))
	for name, value := range values {
		rendered[name] = renderValue(value)
	}
	return rendered
}

// renderValue represents one value as text.
func renderValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case int:
		return strconv.Itoa(typed)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		// A nested value has no text form of its own, so it is written as
		// JSON rather than being dropped.
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return string(encoded)
	}
}
