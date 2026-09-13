package core

import "strings"

// CoreOwner is the reserved registry owner for in-process (core) executors.
const CoreOwner = "neuron"

// CoreNamespace is the second path segment that distinguishes core executors
// from ordinary packages published under the neuron owner. Everything under
// neuron:core runs in-process and is never resolved or installed as a package.
const CoreNamespace = "core"

// CoreTypes lists the executors N.O.R.E. registers in-process at startup.
var CoreTypes = []string{"set", "ai", "command", "delay", "http", "log"}

// CoreName returns the canonical namespaced name for a core executor, e.g.
// CoreName("set") == "neuron:core:set".
func CoreName(name string) ExecutorType {
	return ExecutorType(CoreOwner + ":" + CoreNamespace + ":" + name)
}

// IsCoreExecutorType reports whether t denotes an in-process (core) executor:
// the canonical neuron:core:<name> namespace or a legacy bare core name such
// as "set". "neuron:set" is NOT core; it is a regular package under the
// neuron owner.
func IsCoreExecutorType(t ExecutorType) bool {
	s := string(t)

	for _, n := range CoreTypes {
		if s == n {
			return true
		}
	}

	parts := strings.Split(s, ":")
	if len(parts) >= 3 && parts[0] == CoreOwner && parts[1] == CoreNamespace {
		return true
	}

	return false
}
