package executor

import (
	"fmt"
	"time"

	v1 "github.com/Muhammad-Jay/neuron/shared/protocol/executor/v1"
)

// toProtoValue converts a Go value to a protobuf Value. Supported Go types:
// nil, bool, float64, float32, int, int32, int64, uint, uint32, uint64,
// string, []byte, []any, map[string]any, time.Time, and Stringer.
// Unsupported types are encoded as JSON strings.
func toProtoValue(v any) *v1.Value {
	if v == nil {
		return &v1.Value{Kind: &v1.Value_NullValue{NullValue: v1.NullValue_NULL_VALUE}}
	}

	switch val := v.(type) {
	case bool:
		return &v1.Value{Kind: &v1.Value_BoolValue{BoolValue: val}}
	case string:
		return &v1.Value{Kind: &v1.Value_StringValue{StringValue: val}}
	case float64:
		return &v1.Value{Kind: &v1.Value_NumberValue{NumberValue: val}}
	case float32:
		return &v1.Value{Kind: &v1.Value_NumberValue{NumberValue: float64(val)}}
	case int:
		return &v1.Value{Kind: &v1.Value_NumberValue{NumberValue: float64(val)}}
	case int32:
		return &v1.Value{Kind: &v1.Value_NumberValue{NumberValue: float64(val)}}
	case int64:
		return &v1.Value{Kind: &v1.Value_NumberValue{NumberValue: float64(val)}}
	case uint:
		return &v1.Value{Kind: &v1.Value_NumberValue{NumberValue: float64(val)}}
	case uint32:
		return &v1.Value{Kind: &v1.Value_NumberValue{NumberValue: float64(val)}}
	case uint64:
		return &v1.Value{Kind: &v1.Value_NumberValue{NumberValue: float64(val)}}
	case []byte:
		return &v1.Value{Kind: &v1.Value_StringValue{StringValue: string(val)}}
	case time.Time:
		return &v1.Value{Kind: &v1.Value_StringValue{StringValue: val.Format(time.RFC3339Nano)}}
	case []any:
		items := make([]*v1.Value, 0, len(val))
		for _, item := range val {
			items = append(items, toProtoValue(item))
		}
		return &v1.Value{Kind: &v1.Value_ListValue{ListValue: &v1.ListValue{Values: items}}}
	case map[string]any:
		fields := make(map[string]*v1.Value, len(val))
		for k, item := range val {
			fields[k] = toProtoValue(item)
		}
		return &v1.Value{Kind: &v1.Value_StructValue{StructValue: &v1.Struct{Fields: fields}}}
	default:
		// Fallback: attempt to render as a JSON string.
		return &v1.Value{Kind: &v1.Value_StringValue{StringValue: toJSONString(val)}}
	}
}

// fromProtoValue converts a protobuf Value back to a Go value.
func fromProtoValue(v *v1.Value) any {
	if v == nil {
		return nil
	}

	switch kind := v.Kind.(type) {
	case *v1.Value_NullValue:
		return nil
	case *v1.Value_BoolValue:
		return kind.BoolValue
	case *v1.Value_StringValue:
		return kind.StringValue
	case *v1.Value_NumberValue:
		n := kind.NumberValue
		if n == float64(int64(n)) {
			return int64(n)
		}
		return n
	case *v1.Value_ListValue:
		items := make([]any, 0, len(kind.ListValue.Values))
		for _, item := range kind.ListValue.Values {
			items = append(items, fromProtoValue(item))
		}
		return items
	case *v1.Value_StructValue:
		fields := make(map[string]any, len(kind.StructValue.Fields))
		for k, item := range kind.StructValue.Fields {
			fields[k] = fromProtoValue(item)
		}
		return fields
	default:
		return nil
	}
}

// toJSONString renders an unsupported value as a string for transport.
func toJSONString(v any) string {
	switch val := v.(type) {
	case error:
		return val.Error()
	case fmt.Stringer:
		return val.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}
