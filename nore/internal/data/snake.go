// Package data provides helpers for normalizing runtime payload shapes so
// N.O.R.E. operates on canonical snake_case keys regardless of the casing
// authors chose at the source (for example camelCase --input for a
// TypeScript-authored assembly).
package data

import "github.com/neuron-runtime/neuron/shared/types/core"

// DeepSnakeCase returns a copy of v with every map key converted to
// snake_case, applied recursively to nested maps and slices. Non-map values
// are passed through unchanged, so the operation is idempotent.
func DeepSnakeCase(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, val := range typed {
			out[core.CamelToSnake(k)] = DeepSnakeCase(val)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = DeepSnakeCase(item)
		}
		return out
	default:
		return v
	}
}

// SnakeMap normalizes a map (or map-shaped value) to snake_case keys without
// requiring callers to type-assert the result for the common map[string]any
// case.
func SnakeMap(in map[string]any) map[string]any {
	if out, ok := DeepSnakeCase(in).(map[string]any); ok {
		return out
	}
	return in
}
