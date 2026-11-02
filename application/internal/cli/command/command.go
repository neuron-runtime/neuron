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

	Capability               = "capability"
	CapabilityRuntime        = "runtime"
	CapabilityRuntimeList    = "list"
	CapabilityRuntimeInspect = "inspect [name@version]"

	// Builtins reports the capability runtimes the runtime provides without
	// installation.
	Builtins = "builtins [name]"

	Add    = "add [name@version]"
	Remove = "remove [name@version]"

	New = "new"
)
