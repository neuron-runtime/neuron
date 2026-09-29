package scheduler

import (
	"encoding/json"
	"fmt"

	core2 "github.com/neuron-runtime/neuron/shared/types/core"
)

func validateInput(capability core2.Capability, input map[string]any) error {
	for _, port := range capability.Params {
		value, exists := getPath(input, port.Name)
		if !exists {
			if port.Required {
				return fmt.Errorf("required input %q is missing for capability %s", port.Name, capability.Metadata.ID)
			}
			continue
		}
		if value == nil {
			return fmt.Errorf("input %q is null for capability %s", port.Name, capability.Metadata.ID)
		}
		if !matchesValueType(value, port.Type) {
			return fmt.Errorf("input %q for capability %s expected %s but received %T", port.Name, capability.Metadata.ID, port.Type, value)
		}
	}
	return nil
}

func matchesValueType(value any, valueType core2.ValueType) bool {
	if value == nil {
		return false
	}
	if valueType == core2.ValueAny {
		return true
	}
	switch valueType {
	case core2.ValueString:
		_, ok := value.(string)
		return ok
	case core2.ValueBool:
		_, ok := value.(bool)
		return ok
	case core2.ValueObject:
		_, ok := value.(map[string]any)
		return ok
	case core2.ValueArray:
		_, ok := value.([]any)
		return ok
	case core2.ValueNumber:
		switch value.(type) {
		case int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64,
			float32, float64, json.Number:
			return true
		default:
			return false
		}
	default:
		return false
	}
}
