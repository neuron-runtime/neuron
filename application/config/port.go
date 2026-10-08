package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/spf13/viper"
)

// normalizeRuntimePort resolves the merged runtime.port value — which may
// arrive as an integer or "auto" from a configuration file, as a string from
// NEURON_RUNTIME_PORT, or as either from a command-line override — into the
// single RuntimePort representation the rest of Neuron understands. The
// normalized value is written back into the merge so decoding can never
// observe the union again.
func normalizeRuntimePort(v *viper.Viper) error {
	port, err := coerceRuntimePort(v.Get("runtime.port"))
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	v.Set("runtime.port", port)
	return nil
}

// coerceRuntimePort converts one runtime.port value into a RuntimePort.
// Accepts RuntimePort, "auto" (case-insensitive), an integer (any Go integer
// or JSON/YAML numeric type), or a base-10 string representation of one.
// Fixed ports must be a valid TCP port number.
func coerceRuntimePort(raw any) (RuntimePort, error) {
	switch value := raw.(type) {
	case RuntimePort:
		return value, nil
	case nil:
		return RuntimePort{Mode: RuntimePortAuto}, nil
	case string:
		s := strings.TrimSpace(value)
		if strings.EqualFold(s, string(RuntimePortAuto)) {
			return RuntimePort{Mode: RuntimePortAuto}, nil
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return RuntimePort{}, invalidPortValue(raw)
		}
		return fixedRuntimePort(n)
	case int:
		return fixedRuntimePort(value)
	case int64:
		return fixedRuntimePort(int(value))
	case float64:
		if value != math.Trunc(value) {
			return RuntimePort{}, invalidPortValue(raw)
		}
		return fixedRuntimePort(int(value))
	default:
		return RuntimePort{}, invalidPortValue(raw)
	}
}

func fixedRuntimePort(n int) (RuntimePort, error) {
	if n < 1 || n > 65535 {
		return RuntimePort{}, invalidPortValue(n)
	}
	return RuntimePort{Mode: RuntimePortFixed, Number: n}, nil
}

func invalidPortValue(raw any) error {
	return fmt.Errorf("runtime.port must be \"auto\" or an integer between 1 and 65535, got %v", raw)
}
