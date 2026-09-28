package process

import (
	"time"

	v1 "github.com/Muhammad-Jay/neuron/shared/protocol/capabilityruntime/v1"
)

// defaultExecutionTimeout is the maximum time an execution may run when the
// caller context has no deadline. The runtime enforces this as a defensive
// bound against runaway capability runtimes.
func defaultExecutionTimeout() time.Duration {
	return 10 * time.Minute
}

// makeProtoValue converts a Go value to a protobuf Value for the gRPC
// capability runtime protocol. Nil, bool, numbers, strings, lists, and maps are
// preserved; unsupported values fall back to their string rendering.
func makeProtoValue(v any) *v1.Value {
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
	case []any:
		items := make([]*v1.Value, 0, len(val))
		for _, item := range val {
			items = append(items, makeProtoValue(item))
		}
		return &v1.Value{Kind: &v1.Value_ListValue{ListValue: &v1.ListValue{Values: items}}}
	case map[string]any:
		fields := make(map[string]*v1.Value, len(val))
		for k, item := range val {
			fields[k] = makeProtoValue(item)
		}
		return &v1.Value{Kind: &v1.Value_StructValue{StructValue: &v1.Struct{Fields: fields}}}
	default:
		return &v1.Value{Kind: &v1.Value_StringValue{StringValue: stringify(val)}}
	}
}

// makeGoValue converts a protobuf Value back to a Go value.
func makeGoValue(v *v1.Value) any {
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
			items = append(items, makeGoValue(item))
		}
		return items
	case *v1.Value_StructValue:
		fields := make(map[string]any, len(kind.StructValue.Fields))
		for k, item := range kind.StructValue.Fields {
			fields[k] = makeGoValue(item)
		}
		return fields
	default:
		return nil
	}
}

// stringify renders an unsupported value as a string for protobuf transport.
func stringify(v any) string {
	switch val := v.(type) {
	case error:
		return val.Error()
	case interface{ String() string }:
		return val.String()
	default:
		return "unsupported value"
	}
}
