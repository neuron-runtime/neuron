package manifest

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

// Canonicalize rewrites an authored manifest in place so identifier keys
// follow the canonical snake_case convention and binding mappings carry
// structured sources instead of legacy mapping-expression strings.
//
// Only the casing of identifier tokens changes; operators, numbers, quoted
// string literals and $-prefixed tokens in validation expressions are
// preserved. TS-authored manifests carry camelCase keys inherited from
// JavaScript sources; unifying them on disk means every downstream stage
// treats keys identically regardless of source language.
func Canonicalize(m *Assembly) (*Assembly, error) {
	if m == nil {
		return nil, nil
	}
	for i := range m.Bindings {
		c := &m.Bindings[i]
		for j := range c.Mappings {
			c.Mappings[j].Target = core.CamelToSnake(c.Mappings[j].Target)
			if err := canonicalizeMappingSource(c, j); err != nil {
				return nil, err
			}
		}
		for j := range c.Validations {
			c.Validations[j].Expression = canonicalizeExpression(c.Validations[j].Expression)
		}
	}
	return m, nil
}

// canonicalizeMappingSource replaces a mapping's legacy expression dialect
// with its canonical structured source. A mapping authored with no expression
// and no source cannot be resolved and is rejected here.
func canonicalizeMappingSource(c *Binding, j int) error {
	mapping := &c.Mappings[j]
	if mapping.Source == nil {
		if strings.TrimSpace(mapping.Expression) == "" {
			return fmt.Errorf("binding %s -> %s: mapping target %q has no source expression", c.From, c.To, mapping.Target)
		}
		source, err := core.ParseMappingSource(c.From, mapping.Expression)
		if err != nil {
			return fmt.Errorf("binding %s -> %s: mapping target %q: %w", c.From, c.To, mapping.Target, err)
		}
		mapping.Source = &source
	}
	// Structured sources authored from another surface may still carry
	// camelCase keys; normalize every segment to the canonical snake_case the
	// runtime exposes.
	for k, segment := range mapping.Source.Path {
		mapping.Source.Path[k] = core.CamelToSnake(segment)
	}
	if err := mapping.Source.Validate(); err != nil {
		return fmt.Errorf("binding %s -> %s: mapping target %q: %w", c.From, c.To, mapping.Target, err)
	}
	mapping.Expression = ""
	return nil
}

// canonicalizeExpression rewrites camelCase identifier tokens to snake_case.
func canonicalizeExpression(expression string) string {
	runes := []rune(expression)
	b := make([]rune, 0, len(runes))
	i, n := 0, len(runes)
	for i < n {
		r := runes[i]
		switch {
		case r == '\'' || r == '"':
			quote := r
			b = append(b, quote)
			i++
			for i < n {
				cur := runes[i]
				b = append(b, cur)
				i++
				if cur == '\\' && i < n {
					b = append(b, runes[i])
					i++
					continue
				}
				if cur == quote {
					break
				}
			}
		case isIdentifierStart(r):
			start := i
			for i < n && isIdentifierChar(runes[i]) {
				i++
			}
			word := string(runes[start:i])
			if !strings.HasPrefix(word, "$") {
				if snake := core.CamelToSnake(word); snake != word {
					b = append(b, []rune(snake)...)
					continue
				}
			}
			b = append(b, runes[start:i]...)
		default:
			b = append(b, r)
			i++
		}
	}
	return string(b)
}

func isIdentifierStart(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r)
}

func isIdentifierChar(r rune) bool {
	return isIdentifierStart(r) || unicode.IsDigit(r)
}
