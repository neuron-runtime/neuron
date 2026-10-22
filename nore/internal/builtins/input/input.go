// Package input defines how a built-in capability runtime reads its input.
//
// Every built-in capability runtime receives the same two inputs: the params
// bound to the capability invocation, and the capability's own resolved
// configuration. This package owns the single merge rule that combines them
// and the typed accessors that read a value out of the result, so that every
// built-in interprets its input the same way.
//
// The merge rule is: params override configuration. A param is an explicit
// argument supplied at the call site; a configuration value is the ambient
// default declared alongside the capability. The explicit value wins.
//
// The engine-owned RuntimeConfig is deliberately not part of this input. It
// instructs N.O.R.E. how to drive the capability and is never sent to the
// capability runtime, so a built-in must never read its options from it.
package input

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Format selects how a built-in capability runtime represents the payload it
// produced.
//
// The format is chosen by the caller through the capability's own input, and
// every built-in that produces a payload honors it. JSON is the default
// because a structured value is what an assembly can bind into a downstream
// capability; a stringified object is never a substitute for it.
type Format string

const (
	// FormatJSON represents the payload as structured JSON. The payload is
	// parsed as JSON and returned as the value it represents.
	FormatJSON Format = "json"

	// FormatText represents the payload as a plain string.
	FormatText Format = "text"
)

// Formats lists every supported format, in the order they are reported to a
// user who asks what a built-in accepts.
func Formats() []Format {
	return []Format{FormatJSON, FormatText}
}

// ParseFormat converts a caller-supplied format name into a Format. An empty
// name resolves to fallback, which is how a built-in expresses its default.
//
// An unknown name is an error rather than a silent fallback: a capability that
// asked for a format that does not exist must not quietly receive another one.
func ParseFormat(name string, fallback Format) (Format, error) {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "":
		return fallback, nil
	case string(FormatJSON):
		return FormatJSON, nil
	case string(FormatText):
		return FormatText, nil
	default:
		return fallback, fmt.Errorf("unknown output format %q; supported formats are %s", name, formatList())
	}
}

// Values is a merged, read-only view of a built-in capability runtime's input.
//
// The zero value is not usable; build one with New.
type Values struct {
	// runtime names the built-in that is reading these values. It is used to
	// make every error self-describing, because a capability failure is
	// reported against the capability, not against the runtime engine.
	runtime string

	// values is the merged input: configuration first, params second.
	values map[string]any
}

// New merges a capability's configuration and its params into one value set.
//
// Configuration supplies the ambient defaults declared alongside the
// capability. Params supply the explicit arguments bound to the invocation
// and override them, so the same capability behaves the same way whether it
// is invoked with explicit arguments or left to its declared defaults.
//
// Neither map is retained or mutated.
func New(runtime string, configurations map[string]any, params map[string]any) Values {
	merged := make(map[string]any, len(configurations)+len(params))
	for key, value := range configurations {
		merged[key] = value
	}
	for key, value := range params {
		merged[key] = value
	}
	return Values{runtime: runtime, values: merged}
}

// Runtime returns the name of the built-in reading these values.
func (v Values) Runtime() string {
	return v.runtime
}

// Has reports whether the named key carries a value.
func (v Values) Has(name string) bool {
	_, ok := v.values[name]
	return ok
}

// Keys returns the names present in the merged input, sorted, so that an
// error listing the available names is stable.
func (v Values) Keys() []string {
	keys := make([]string, 0, len(v.values))
	for key := range v.values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// All returns the merged input as a plain map.
//
// The returned map is a copy, so a caller that returns it as a capability
// result cannot be affected by a later change to the input.
func (v Values) All() map[string]any {
	merged := make(map[string]any, len(v.values))
	for key, value := range v.values {
		merged[key] = value
	}
	return merged
}

// Raw returns the value stored under name and whether it was present. It is
// the escape hatch for values this package does not model, such as an
// arbitrary JSON body.
func (v Values) Raw(name string) (any, bool) {
	value, ok := v.values[name]
	return value, ok
}

// String returns the value stored under name as a string.
//
// A present value that is not a string is an error rather than a coercion: a
// number silently turned into text is how a duration ends up parsed as
// "30000000000" and a URL ends up sent with a missing scheme.
func (v Values) String(name string) (string, bool, error) {
	value, ok := v.values[name]
	if !ok || value == nil {
		return "", false, nil
	}
	text, ok := value.(string)
	if !ok {
		return "", true, v.typeError(name, "a string", value)
	}
	return text, true, nil
}

// RequiredString returns the value stored under name as a string and fails
// when it is absent or empty.
func (v Values) RequiredString(name string) (string, error) {
	text, present, err := v.String(name)
	if err != nil {
		return "", err
	}
	if !present {
		return "", v.missingError(name)
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%s: required parameter %q is empty", v.runtime, name)
	}
	return text, nil
}

// Bool returns the value stored under name as a boolean.
//
// Only a real boolean is accepted. The strings "true" and "false" are not,
// because a capability that receives "false" as text has a defect in the
// capability's data, and coercing it hides that defect.
func (v Values) Bool(name string, fallback bool) (bool, error) {
	value, ok := v.values[name]
	if !ok || value == nil {
		return fallback, nil
	}
	flag, ok := value.(bool)
	if !ok {
		return false, v.typeError(name, "a boolean", value)
	}
	return flag, nil
}

// Int returns the value stored under name as an integer.
//
// JSON has a single number type, so an integer arrives as a float64. A value
// with a fractional part is rejected, because truncating it would silently
// change what the author asked for.
func (v Values) Int(name string, fallback int) (int, error) {
	value, ok := v.values[name]
	if !ok || value == nil {
		return fallback, nil
	}
	switch typed := value.(type) {
	case int:
		return typed, nil
	case int32:
		return int(typed), nil
	case int64:
		return int(typed), nil
	case float64:
		if typed != float64(int(typed)) {
			return 0, v.typeError(name, "a whole number", value)
		}
		return int(typed), nil
	case float32:
		if typed != float32(int(typed)) {
			return 0, v.typeError(name, "a whole number", value)
		}
		return int(typed), nil
	default:
		return 0, v.typeError(name, "a whole number", value)
	}
}

// Duration returns the value stored under name as a duration and falls back
// when it is absent.
//
// A duration is written as a string such as "30s" or "2m30s" rather than as a
// number of nanoseconds, so the value survives the JSON round trip between
// the compiler, the manifest, and the runtime without losing its meaning.
func (v Values) Duration(name string, fallback time.Duration) (time.Duration, error) {
	value, ok := v.values[name]
	if !ok || value == nil {
		return fallback, nil
	}
	text, ok := value.(string)
	if !ok {
		return 0, v.typeError(name, `a duration string such as "30s"`, value)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(text)
	if err != nil {
		return 0, fmt.Errorf("%s: parameter %q must be a duration string such as \"30s\", got %q", v.runtime, name, text)
	}
	return duration, nil
}

// StringMap returns the value stored under name as a map of strings.
//
// Every value must be a string. A header map or an environment map with a
// number in it is a defect in the capability's data, and dropping the
// offending entry silently would produce a request that is missing exactly
// the part the author cared about.
func (v Values) StringMap(name string) (map[string]string, bool, error) {
	value, ok := v.values[name]
	if !ok || value == nil {
		return nil, false, nil
	}
	source, ok := value.(map[string]any)
	if !ok {
		return nil, true, v.typeError(name, "an object of string values", value)
	}
	result := make(map[string]string, len(source))
	for key, item := range source {
		text, ok := item.(string)
		if !ok {
			return nil, true, fmt.Errorf("%s: parameter %q entry %q must be a string, got %s", v.runtime, name, key, describe(item))
		}
		result[key] = text
	}
	return result, true, nil
}

// StringSlice returns the value stored under name as a list of strings.
//
// A JSON array arrives as []any. Every element must be a string; a number in
// an argument list is a defect in the capability's data.
func (v Values) StringSlice(name string) ([]string, bool, error) {
	value, ok := v.values[name]
	if !ok || value == nil {
		return nil, false, nil
	}
	source, ok := value.([]any)
	if !ok {
		return nil, true, v.typeError(name, "an array of strings", value)
	}
	result := make([]string, 0, len(source))
	for index, item := range source {
		text, ok := item.(string)
		if !ok {
			return nil, true, fmt.Errorf("%s: parameter %q entry %d must be a string, got %s", v.runtime, name, index, describe(item))
		}
		result = append(result, text)
	}
	return result, true, nil
}

// Object returns the value stored under name as an object.
//
// The returned map is the nested value itself, not a copy of it.
func (v Values) Object(name string) (map[string]any, bool, error) {
	value, ok := v.values[name]
	if !ok || value == nil {
		return nil, false, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, v.typeError(name, "an object", value)
	}
	return object, true, nil
}

// Nested returns the object stored under name as a set of values.
//
// The nested values are read with the same rules as the outer ones, so a
// built-in does not have to know how the fields of a nested object are typed.
func (v Values) Nested(name string) (Values, bool, error) {
	object, present, err := v.Object(name)
	if err != nil || !present {
		return Values{runtime: v.runtime}, present, err
	}
	return New(v.runtime, nil, object), true, nil
}

// Format returns the output format selected by the parameter named name,
// falling back when it is absent.
func (v Values) Format(name string, fallback Format) (Format, error) {
	text, present, err := v.String(name)
	if err != nil {
		return fallback, err
	}
	if !present {
		return fallback, nil
	}
	return ParseFormat(text, fallback)
}

// missingError reports an absent required parameter and names the values that
// are present, so the author can see what the capability did receive.
func (v Values) missingError(name string) error {
	keys := v.Keys()
	if len(keys) == 0 {
		return fmt.Errorf("%s: required parameter %q is missing and the capability received no input", v.runtime, name)
	}
	return fmt.Errorf("%s: required parameter %q is missing; received %s", v.runtime, name, strings.Join(keys, ", "))
}

// typeError reports a value of the wrong type and states what was expected.
func (v Values) typeError(name, expected string, value any) error {
	return fmt.Errorf("%s: parameter %q must be %s, got %s", v.runtime, name, expected, describe(value))
}

// describe names the type of a value so an error can say what it received.
func describe(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case float64, float32, int, int32, int64:
		return "a number"
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	default:
		return fmt.Sprintf("%T", value)
	}
}

// formatList names every supported format for an error message.
func formatList() string {
	names := make([]string, 0, len(Formats()))
	for _, format := range Formats() {
		names = append(names, string(format))
	}
	return strings.Join(names, ", ")
}
