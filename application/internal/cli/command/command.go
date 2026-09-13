package command

var (
	Neuron = "neuron"

	Run = "run [instance-id|system-key]"

	Init = "init [Target]"

	Instance       = "instance"
	InstanceList   = "list [instance-id]"
	InstanceRemove = "remove [instance-id|system-key]"
	InstanceClear  = "clear"

	Register = "register"

	// Build is the canonical command that takes a project from source to a
	// registered, runnable system. `register` is its deprecated alias.
	Build = "build"

	Version = "version"

	Daemon = "daemon"

	Executor        = "executor"
	ExecutorList    = "list"
	ExecutorInspect = "inspect [name@version]"

	Add    = "add [name@version]"
	Remove = "remove [name@version]"
)
