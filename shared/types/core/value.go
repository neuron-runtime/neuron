package core

type ExecutorType string
type ResourceType string
type ValueType string

const (
	ValueAny    ValueType = "any"
	ValueString ValueType = "string"
	ValueNumber ValueType = "number"
	ValueBool   ValueType = "bool"
	ValueObject ValueType = "object"
	ValueArray  ValueType = "array"
)

type Port struct {
	Name     string
	Type     ValueType
	Required bool
}

type Endpoint struct {
	ServiceID ID
	Port      string
}
