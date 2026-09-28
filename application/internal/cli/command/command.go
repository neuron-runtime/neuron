package command

var (
	Neuron = "neuron"

	Run = "run [instance-id|assembly-key]"

	Init = "init [Target]"

	Instance       = "instance"
	InstanceList   = "list [instance-id]"
	InstanceRemove = "remove [instance-id|assembly-key]"
	InstanceClear  = "clear"

	Register = "register"

	// Build is the canonical command that takes a project from source to a
	// registered, runnable assembly. `register` is its deprecated alias.
	Build = "build"

	Version = "version"

	Daemon = "daemon"

	CapabilityRuntime        = "capability runtime"
	CapabilityRuntimeList    = "list"
	CapabilityRuntimeInspect = "inspect [name@version]"

	Add    = "add [name@version]"
	Remove = "remove [name@version]"
)
