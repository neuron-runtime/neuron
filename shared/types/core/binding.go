package core

import (
	"encoding/json"
	"fmt"
)

// MappingRule maps a value from the source Capability/execution context into
// a target Capability param path.
//
// Source is the canonical structured reference for the mapped value. Rules
// loaded from artifacts produced before the structured form existed carry the
// legacy expression string instead; SourceRef resolves it the first time a
// plan is compiled, so persisted registrations keep working without a rewrite.
type MappingRule struct {
	TargetPath string    `json:"targetPath"`
	Source     *ValueRef `json:"source,omitempty"`
	// legacyExpression holds the pre-structure dialect string captured during
	// unmarshaling. It is never marshaled.
	legacyExpression string `json:"-"`
}

// UnmarshalJSON accepts both the current structured form and the legacy
// {"TargetPath": ..., "Expression": "..."} form that persisted registrations
// were written in, so old artifacts load without migration.
func (m *MappingRule) UnmarshalJSON(data []byte) error {
	var wire struct {
		TargetPath string    `json:"targetPath"`
		Expression string    `json:"expression"`
		Source     *ValueRef `json:"source"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	m.TargetPath = wire.TargetPath
	m.Source = wire.Source
	m.legacyExpression = wire.Expression
	return nil
}

// SourceRef returns the effective structured source of the rule. A rule
// loaded from a pre-structure artifact has its legacy expression string
// resolved here, qualified with the identity of the Capability the binding
// originates from.
func (m *MappingRule) SourceRef(fromCapability string) (ValueRef, error) {
	if m.Source != nil {
		return *m.Source, nil
	}
	if m.legacyExpression == "" {
		return ValueRef{}, fmt.Errorf("mapping for target %q has no source", m.TargetPath)
	}
	ref, err := ParseMappingSource(fromCapability, m.legacyExpression)
	if err != nil {
		return ValueRef{}, fmt.Errorf("mapping for target %q: %w", m.TargetPath, err)
	}
	return ref, nil
}

// ValidationRule is an optional transition assertion evaluated against
// the source Capability result and execution context.
// The expression must return bool.
type ValidationRule struct {
	Expression string
	Message    string
}

// Binding has three responsibilities only:
//  1. establish execution order;
//  2. optionally map source result into target params;
//  3. optionally assert that the transition is allowed.
type Binding struct {
	Metadata Metadata

	From Endpoint
	To   Endpoint

	Mappings    []MappingRule
	Validations []ValidationRule
}
