package data

import "math"

// CanonicalValue returns a deep copy of value in the one representation
// N.O.R.E. uses for every runtime payload.
//
// The engine accepts payloads from several places that each have their own Go
// types: an HTTP request body decoded into `map[string]any` yields float64 for
// every number, a CEL expression yields int64 or float64 depending on whether
// the value was computed or copied out of a native map, and a capability
// runtime's JSON response yields float64 again. Left alone, the same logical
// number reaches an in-process capability as `int`, `int64`, or `float64`
// depending on how the author wrote an expression, and an execution restored
// from a snapshot observes different types than the same execution before it
// was persisted.
//
// CanonicalValue removes that ambiguity: on the way in, every JSON number
// becomes the float64 that `encoding/json` produces, and every container is
// copied so no caller can mutate engine state through a returned payload.
//
// Values outside the JSON data model are passed through unchanged, because the
// engine does not own them and rejecting them here would fail a payload that
// JSON would have carried losslessly.
func CanonicalValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil

	case bool, string:
		return typed

	// Every JSON number is a float64. Integers narrower than float64's exact
	// range convert without loss; beyond 2^53 the value has already lost
	// precision at the JSON boundary, so nothing is given up here.
	case int:
		return float64(typed)
	case int8:
		return float64(typed)
	case int16:
		return float64(typed)
	case int32:
		return float64(typed)
	case int64:
		return float64(typed)
	case uint:
		return float64(typed)
	case uint8:
		return float64(typed)
	case uint16:
		return float64(typed)
	case uint32:
		return float64(typed)
	case uint64:
		return float64(typed)
	case float32:
		return float64(typed)
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			// JSON cannot represent these, so a payload carrying one could not
			// survive persistence. Drop it rather than let it reach a
			// capability runtime as an unencodable value.
			return nil
		}
		return typed

	case map[string]any:
		return CanonicalMap(typed)

	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = CanonicalValue(item)
		}
		return result

	default:
		return typed
	}
}

// CanonicalMap returns a canonicalized deep copy of source. A nil source
// yields an empty map rather than nil, so callers that read from the result
// never have to distinguish "absent" from "empty".
func CanonicalMap(source map[string]any) map[string]any {
	if source == nil {
		return map[string]any{}
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = CanonicalValue(value)
	}
	return result
}
