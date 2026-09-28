package core

type Assembly struct {
	Metadata      Metadata
	Specification AssemblySpec
}

type AssemblySpec struct {
	Capabilities []Capability
	Triggers     []Trigger
	Bindings     []Binding
}
