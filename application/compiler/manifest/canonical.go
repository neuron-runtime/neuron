package manifest

import (
	"strings"
	"unicode"

	"github.com/Muhammad-Jay/neuron/shared/types/core"
)

// Canonicalize rewrites binding mapping targets and expressions in place so
// identifier keys follow the canonical snake_case convention. Only the casing
// of identifier tokens changes; operators, numbers, quoted string literals and
// $-prefixed tokens are preserved. TS-authored manifests carry camelCase keys
// inherited from JavaScript sources; unifying them on disk means every
// downstream stage treats keys identically regardless of source language.
func Canonicalize(m *Assembly) *Assembly {
	if m == nil {
		return nil
	}
	for i := range m.Bindings {
		c := &m.Bindings[i]
		for j := range c.Mappings {
			c.Mappings[j].Target = core.CamelToSnake(c.Mappings[j].Target)
			c.Mappings[j].Expression = canonicalizeExpression(c.Mappings[j].Expression)
		}
		for j := range c.Validations {
			c.Validations[j].Expression = canonicalizeExpression(c.Validations[j].Expression)
		}
	}
	return m
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
