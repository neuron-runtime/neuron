package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

type configurationProgram struct {
	root configurationNode
}

type configurationNode interface {
	resolve(ctx context.Context, environment CapabilityEnvironment) (any, error)
}

type literalNode struct {
	value any
}

type objectNode map[string]configurationNode
type arrayNode []configurationNode

type templateNode struct {
	raw      string
	exact    bool
	segments []templateSegment
	maxSize  int
}

type templateSegment interface {
	isTemplateSegment()
}

type textSegment struct {
	text string
}

type expressionSegment struct {
	source  string
	program *celExpression
}

func (textSegment) isTemplateSegment()       {}
func (expressionSegment) isTemplateSegment() {}

func (p *configurationProgram) Resolve(ctx context.Context, environment CapabilityEnvironment) (map[string]any, error) {
	value, err := p.root.resolve(ctx, environment)
	if err != nil {
		return nil, err
	}
	resolved, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("resolved Capability configuration must be an object, received %T", value)
	}
	return resolved, nil
}

func (c *celCompiler) compileConfigurationValue(value any, path string) (configurationNode, error) {
	if value == nil {
		return literalNode{value: nil}, nil
	}

	ref := reflect.ValueOf(value)
	if ref.Kind() == reflect.Interface {
		ref = ref.Elem()
	}

	switch ref.Kind() {
	case reflect.String:
		return c.compileTemplate(ref.String(), path)

	case reflect.Map:
		if ref.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("configuration %s uses a map with non-string keys", path)
		}
		result := make(objectNode, ref.Len())
		iterator := ref.MapRange()
		for iterator.Next() {
			key := iterator.Key().String()
			child, err := c.compileConfigurationValue(iterator.Value().Interface(), path+"."+key)
			if err != nil {
				return nil, err
			}
			result[key] = child
		}
		return result, nil

	case reflect.Slice, reflect.Array:
		result := make(arrayNode, ref.Len())
		for index := 0; index < ref.Len(); index++ {
			child, err := c.compileConfigurationValue(ref.Index(index).Interface(), fmt.Sprintf("%s[%d]", path, index))
			if err != nil {
				return nil, err
			}
			result[index] = child
		}
		return result, nil

	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return literalNode{value: value}, nil

	default:
		return nil, fmt.Errorf("configuration %s contains unsupported value type %T", path, value)
	}
}

func (c *celCompiler) compileTemplate(raw string, path string) (configurationNode, error) {
	if len(raw) > c.config.MaxTemplateSize {
		return nil, fmt.Errorf("configuration template %s exceeds %d bytes", path, c.config.MaxTemplateSize)
	}

	segments := make([]templateSegment, 0, 4)
	cursor := 0
	expressionCount := 0

	for cursor < len(raw) {
		relativeStart := strings.Index(raw[cursor:], "{{")
		if relativeStart < 0 {
			if strings.Contains(raw[cursor:], "}}") {
				return nil, fmt.Errorf("configuration template %s contains an unmatched closing delimiter", path)
			}
			if cursor < len(raw) {
				segments = append(segments, textSegment{text: raw[cursor:]})
			}
			break
		}

		start := cursor + relativeStart
		if start > cursor {
			segments = append(segments, textSegment{text: raw[cursor:start]})
		}

		expressionStart := start + 2
		relativeEnd := strings.Index(raw[expressionStart:], "}}")
		if relativeEnd < 0 {
			return nil, fmt.Errorf("configuration template %s contains an unclosed expression", path)
		}
		end := expressionStart + relativeEnd
		expression := strings.TrimSpace(raw[expressionStart:end])
		if expression == "" {
			return nil, fmt.Errorf("configuration template %s contains an empty expression", path)
		}
		if strings.Contains(expression, "{{") {
			return nil, fmt.Errorf("configuration template %s contains nested template delimiters", path)
		}

		expressionCount++
		if expressionCount > c.config.MaxTemplateExpressions {
			return nil, fmt.Errorf("configuration template %s exceeds %d expressions", path, c.config.MaxTemplateExpressions)
		}

		program, err := c.compileExpression(c.capabilityEnv, expression)
		if err != nil {
			return nil, fmt.Errorf("configuration template %s: %w", path, err)
		}
		segments = append(segments, expressionSegment{source: expression, program: program})
		cursor = end + 2
	}

	if expressionCount == 0 {
		return literalNode{value: raw}, nil
	}

	exact := len(segments) == 1
	if exact {
		_, exact = segments[0].(expressionSegment)
	}

	return templateNode{
		raw:      raw,
		exact:    exact,
		segments: segments,
		maxSize:  c.config.MaxResolvedStringSize,
	}, nil
}

func (n literalNode) resolve(_ context.Context, _ CapabilityEnvironment) (any, error) {
	return cloneJSONLike(n.value), nil
}

func (n objectNode) resolve(ctx context.Context, environment CapabilityEnvironment) (any, error) {
	result := make(map[string]any, len(n))
	for key, child := range n {
		value, err := child.resolve(ctx, environment)
		if err != nil {
			return nil, fmt.Errorf("resolve configuration field %q: %w", key, err)
		}
		result[key] = value
	}
	return result, nil
}

func (n arrayNode) resolve(ctx context.Context, environment CapabilityEnvironment) (any, error) {
	result := make([]any, len(n))
	for index, child := range n {
		value, err := child.resolve(ctx, environment)
		if err != nil {
			return nil, fmt.Errorf("resolve configuration item %d: %w", index, err)
		}
		result[index] = value
	}
	return result, nil
}

func (n templateNode) resolve(ctx context.Context, environment CapabilityEnvironment) (any, error) {
	variables := map[string]any{
		"params":     environment.Params,
		"execution":  environment.Execution,
		"capability": environment.Capability,
	}

	if n.exact {
		segment := n.segments[0].(expressionSegment)
		value, err := segment.program.evaluate(ctx, variables)
		if err != nil {
			return nil, err
		}
		if value == nil {
			return nil, fmt.Errorf("expression %q resolved to null", segment.source)
		}
		return cloneJSONLike(value), nil
	}

	var builder strings.Builder
	builder.Grow(len(n.raw))

	for _, segment := range n.segments {
		switch typed := segment.(type) {
		case textSegment:
			builder.WriteString(typed.text)

		case expressionSegment:
			value, err := typed.program.evaluate(ctx, variables)
			if err != nil {
				return nil, err
			}
			text, err := stringifyTemplateValue(value)
			if err != nil {
				return nil, fmt.Errorf("expression %q: %w", typed.source, err)
			}
			builder.WriteString(text)
		}

		if builder.Len() > n.maxSize {
			return nil, fmt.Errorf("resolved template exceeds %d bytes", n.maxSize)
		}
	}

	return builder.String(), nil
}

func stringifyTemplateValue(value any) (string, error) {
	if value == nil {
		return "", fmt.Errorf("resolved to null")
	}

	switch typed := value.(type) {
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	case bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return fmt.Sprint(typed), nil
	case map[string]any, []any:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return "", fmt.Errorf("encode value as JSON: %w", err)
		}
		return string(encoded), nil
	default:
		return fmt.Sprint(typed), nil
	}
}

func cloneJSONLike(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = cloneJSONLike(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = cloneJSONLike(item)
		}
		return result
	default:
		return typed
	}
}
