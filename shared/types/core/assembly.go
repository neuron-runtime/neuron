package core

type Assembly struct {
	// Metadata carries the Assembly identity (Name, Version) and any labels.
	Metadata Metadata

	// Params declare the Assembly's public invocation contract. They live on
	// the Assembly rather than inside the Specification because they describe
	// the Assembly's own input contract — parallel to Metadata — and must
	// travel with the Assembly when it is registered and hashed, independent
	// of how the Specification schedules execution.
	Params []Port `json:"params,omitempty"`

	// Result declares the Assembly's public result contract and the structured
	// mapping that produces each field. Like Params it is part of the
	// Assembly's serialized identity and its registered contract, so it lives
	// beside Metadata, not inside the execution Specification.
	Result *AssemblyResult `json:"result,omitempty"`

	Specification AssemblySpec
}

// AssemblyResult is the public result contract of an Assembly execution: the
// schema callers read after a synchronous run, and the structured mapping from
// capability results (and assembly params, project variables, and literals) to
// each declared field. Field order is the author's declared contract and is
// preserved verbatim.
type AssemblyResult struct {
	Schema []Port
	Fields []AssemblyResultField
}

// AssemblyResultField maps one declared Assembly result field to the value
// that must produce it at execution time.
type AssemblyResultField struct {
	Name   string
	Source *ValueRef
}

type AssemblySpec struct {
	Capabilities []Capability
	Triggers     []Trigger
	Bindings     []Binding
}
