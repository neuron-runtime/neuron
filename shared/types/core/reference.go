package core

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// ValueRefKind names which source a ValueRef reads from. The kind is the
// discriminator of the structured-reference union: it distinguishes where a
// value comes from, while Capability and Path say which value it is.
type ValueRefKind string

const (
	// ValueRefCapabilityResult reads a field path from the completed result of
	// the referenced Capability.
	ValueRefCapabilityResult ValueRefKind = "capabilityResult"

	// ValueRefCapabilityParams reads a field path from the referenced
	// Capability's own invocation params.
	ValueRefCapabilityParams ValueRefKind = "capabilityParams"

	// ValueRefAssemblyParams reads a field path from the Assembly's initial
	// params.
	ValueRefAssemblyParams ValueRefKind = "assemblyParams"

	// ValueRefVariable reads a named project variable. No authoring surface
	// emits this kind yet; it exists so the union can distinguish variables
	// from params and results rather than folding them into one of those.
	ValueRefVariable ValueRefKind = "variable"

	// ValueRefLiteral carries an inline value with no lookup.
	ValueRefLiteral ValueRefKind = "literal"
)

// ValueRef is the canonical structured reference for a mapped value. It
// replaces the opaque expression strings that used to be the canonical
// representation of a Capability reference: Capability identity and the
// referenced field path are separate, structured data, so consumers never
// parse a string to learn what a mapping reads.
//
// JSON form (this is what a canonical manifest serializes):
//
//	{"kind":"capabilityResult","capability":"validate-order","path":["order","id"]}
//	{"kind":"assemblyParams","path":["order","currency"]}
//	{"kind":"literal","value":"fixed"}
//
// Path segments are canonical snake_case identifiers, matching the key casing
// the runtime exposes for every payload variable.
type ValueRef struct {
	Kind ValueRefKind `json:"kind"`

	// Capability is the identity of the Capability a capability-scoped
	// reference reads from. It is empty for assemblyParams, variable, and
	// literal references.
	Capability string `json:"capability,omitempty"`

	// Path is the field path inside the referenced source. An empty path
	// selects the whole source (for example the Capability's entire result).
	Path []string `json:"path,omitempty"`

	// Value holds the inline value of a literal reference.
	Value any `json:"value,omitempty"`
}

// Validate reports whether the reference is internally consistent: a known
// kind, a Capability identity exactly where the kind requires one, and no
// path segment that cannot name a field.
func (r ValueRef) Validate() error {
	switch r.Kind {
	case ValueRefCapabilityResult, ValueRefCapabilityParams:
		if r.Capability == "" {
			return fmt.Errorf("%s reference requires a capability identity", r.Kind)
		}
	case ValueRefAssemblyParams, ValueRefVariable, ValueRefLiteral:
		if r.Capability != "" {
			return fmt.Errorf("%s reference must not carry a capability identity", r.Kind)
		}
	default:
		return fmt.Errorf("unknown reference kind %q", r.Kind)
	}
	for _, segment := range r.Path {
		if !isPathSegment(segment) {
			return fmt.Errorf("%s reference path contains an invalid segment %q", r.Kind, segment)
		}
	}
	return nil
}

// isPathSegment reports whether a segment can name a field: identifiers,
// digits, underscores, hyphens and $-prefixed tokens, with no whitespace or
// operator characters. A segment that carried `>=`, `+`, or other expression
// syntax is not a field path.
func isPathSegment(segment string) bool {
	if segment == "" {
		return false
	}
	for _, r := range segment {
		if r == '_' || r == '$' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return true
}

// ParseMappingSource converts a legacy mapping expression string into the
// structured ValueRef the rest of the pipeline consumes. It is the single
// authoritative conversion: it accepts exactly the reference forms the
// authoring dialect could emit, normalizes path segments to canonical
// snake_case, and rejects anything else instead of letting an opaque string
// reach the compiler.
//
// fromCapability is the identity of the Capability the binding originates
// from; `source.*` references are qualified with it, which is how a relative
// dialect expression acquires the explicit Capability identity the structured
// form requires.
func ParseMappingSource(fromCapability string, expression string) (ValueRef, error) {
	expr := strings.TrimSpace(expression)
	if expr == "" {
		return ValueRef{}, fmt.Errorf("mapping expression is empty")
	}

	if ref, ok, err := parseReference(fromCapability, expr); ok {
		return ref, err
	}

	literal, ok := parseLiteral(expr)
	if !ok {
		return ValueRef{}, fmt.Errorf(
			"mapping expression %q is not a supported reference; expected source.result.<path>, source.params.<path>, execution.params.<path>, or a literal",
			expression,
		)
	}
	return ValueRef{Kind: ValueRefLiteral, Value: literal}, nil
}

// referencePrefixes lists the dialect roots ParseMappingSource understands,
// in the order a string must be matched: `source.result.x` must not be
// consumed by a shorter `source` prefix.
var referencePrefixes = []struct {
	prefix string
	kind   ValueRefKind
	// sourcePrefix marks references qualified with the binding's from
	// Capability (`source.*`); they are rejected when no identity is known.
	sourcePrefix bool
}{
	{prefix: "source.result", kind: ValueRefCapabilityResult, sourcePrefix: true},
	{prefix: "source.params", kind: ValueRefCapabilityParams, sourcePrefix: true},
	{prefix: "execution.params", kind: ValueRefAssemblyParams},
}

func parseReference(fromCapability, expr string) (ValueRef, bool, error) {
	for _, candidate := range referencePrefixes {
		if expr != candidate.prefix && !strings.HasPrefix(expr, candidate.prefix+".") {
			continue
		}
		if candidate.sourcePrefix && fromCapability == "" {
			return ValueRef{}, true, fmt.Errorf(
				"reference %q needs the identity of the Capability it reads from", expr,
			)
		}
		remainder := strings.TrimPrefix(expr, candidate.prefix)
		remainder = strings.TrimPrefix(remainder, ".")
		ref := ValueRef{Kind: candidate.kind}
		if candidate.sourcePrefix {
			ref.Capability = fromCapability
		}
		if remainder != "" {
			segments := strings.Split(remainder, ".")
			for index, segment := range segments {
				segment = strings.TrimSpace(segment)
				if !isPathSegment(segment) {
					return ValueRef{}, true, fmt.Errorf("reference %q path contains an invalid segment %q", expr, segment)
				}
				// Canonicalize every segment so a legacy authored-casing path
				// and a structured path resolve against the same snake_case
				// payload keys the runtime exposes.
				segments[index] = CamelToSnake(segment)
			}
			ref.Path = segments
		}
		return ref, true, nil
	}
	return ValueRef{}, false, nil
}

// parseLiteral recognizes the scalar forms the authoring dialect serializes
// for inline values: booleans, null, quoted strings, and numbers.
func parseLiteral(expr string) (any, bool) {
	switch expr {
	case "true":
		return true, true
	case "false":
		return false, true
	case "null":
		return nil, true
	}
	if len(expr) >= 2 {
		if (expr[0] == '\'' && expr[len(expr)-1] == '\'') ||
			(expr[0] == '"' && expr[len(expr)-1] == '"') {
			unquoted, err := strconv.Unquote(expr)
			if err == nil {
				return unquoted, true
			}
			// Single quotes are not a Go string literal form; strip them
			// directly after honoring backslash escapes.
			return unescapeSingleQuoted(expr[1 : len(expr)-1]), true
		}
	}
	if number, err := strconv.ParseFloat(expr, 64); err == nil {
		return number, true
	}
	return nil, false
}

func unescapeSingleQuoted(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case '\\', '\'', '"':
				b.WriteByte(s[i])
			default:
				b.WriteByte('\\')
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
