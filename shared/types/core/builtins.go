package core

import (
	"fmt"
	"sort"
	"strings"
)

// Built-in capability runtimes are the capabilities N.O.R.E. provides without
// installation. They are declared here so the runtime, the compiler, and the
// CLI describe the same set from one authoritative list.
//
// A built-in qualifies only when it provides a meaningful execution or
// integration boundary — access to a system resource — rather than a generic
// transformation an author could implement in a capability of their own.
//
// The declarations below are the published contract of each built-in. They
// describe the inputs a built-in accepts, the outputs it produces, and the
// boundaries the runtime enforces on its behalf.

// BuiltinParameter describes one input or output of a built-in capability
// runtime.
type BuiltinParameter struct {
	// Name is the parameter or result name.
	Name string

	// Type describes the accepted value in prose, for display purposes.
	Type string

	// Required reports whether the value must be supplied.
	Required bool

	// Default describes the value used when the parameter is absent. An empty
	// string means the parameter has no default and is required.
	Default string

	// Description explains what the parameter controls.
	Description string
}

// BuiltinDescriptor describes one built-in capability runtime.
type BuiltinDescriptor struct {
	// Name is the built-in's name within the core namespace, for example
	// "http" for neuron:core:http.
	Name string

	// Summary is a one-line description of the built-in.
	Summary string

	// Description explains the built-in's purpose and its boundary.
	Description string

	// Parameters are the accepted inputs, in the order they are documented.
	Parameters []BuiltinParameter

	// Results are the produced outputs, in the order they are documented.
	Results []BuiltinParameter

	// Constraints are the security boundaries the runtime enforces.
	Constraints []string
}

// Type returns the canonical capability runtime type of the built-in.
func (d BuiltinDescriptor) Type() CapabilityRuntimeType {
	return CoreName(d.Name)
}

// builtinDescriptors is the authoritative list of built-in capability
// runtimes.
//
// It is a single source of truth: the runtime registers exactly these
// built-ins, the compiler rejects a reference to any other name in the core
// namespace, and the CLI reports exactly these contracts.
var builtinDescriptors = []BuiltinDescriptor{
	{
		Name:    "set",
		Summary: "Materializes a capability's static value set",
		Description: "set combines the capability's own configuration with the params bound to the " +
			"invocation and returns the combination, with params overriding configuration. It is the " +
			"default runtime for a capability that declares none, which makes it the way an assembly " +
			"supplies a value set to a downstream capability without writing a capability runtime.",
		Parameters: []BuiltinParameter{
			{
				Name:        "format",
				Type:        "string",
				Default:     "json",
				Description: "How the values of the set are represented: \"json\" or \"text\".",
			},
		},
		Results: []BuiltinParameter{
			{
				Name:        "value",
				Type:        "object",
				Required:    true,
				Description: "The merged value set: the capability's configuration with the params overlaid on it. The result of the capability carries the names of the merged set.",
			},
		},
		Constraints: []string{
			"Reads only the capability's own input; it reaches nothing outside the runtime.",
			"The format is an instruction to the built-in rather than a value of the set, so it is not returned as one.",
		},
	},
	{
		Name:    "http",
		Summary: "Makes an HTTP request",
		Description: "http performs a single HTTP request and returns the status, the response headers, " +
			"and the body. It is the general-purpose integration boundary: an author who needs to talk " +
			"to an HTTP API should not have to write a capability runtime for it.",
		Parameters: []BuiltinParameter{
			{
				Name:        "url",
				Type:        "string",
				Required:    true,
				Description: "The absolute URL to request.",
			},
			{
				Name:        "method",
				Type:        "string",
				Default:     "GET",
				Description: "The HTTP method.",
			},
			{
				Name:        "headers",
				Type:        "object of strings",
				Description: "Request headers.",
			},
			{
				Name:        "query",
				Type:        "object of strings",
				Description: "Query string parameters, appended to the URL.",
			},
			{
				Name:        "body",
				Type:        "any",
				Description: "The request body, encoded as JSON.",
			},
			{
				Name:        "contentType",
				Type:        "string",
				Default:     "application/json",
				Description: "The request content type. Only used when a body is present.",
			},
			{
				Name:        "auth",
				Type:        "object",
				Description: "Credentials for the request: {\"type\": \"bearer\" | \"basic\" | \"apiKey\", ...}.",
			},
			{
				Name:        "timeout",
				Type:        "duration string",
				Description: "How long the request may take, such as \"30s\".",
			},
			{
				Name:        "format",
				Type:        "string",
				Default:     "json",
				Description: "How the response body is represented: \"json\" or \"text\".",
			},
			{
				Name:        "followRedirects",
				Type:        "boolean",
				Default:     "false",
				Description: "Whether redirects are followed. Each hop is checked against the address policy.",
			},
			{
				Name:        "retry",
				Type:        "object",
				Description: "Retry behavior: {\"attempts\": n, \"backoff\": \"200ms\", \"methods\": [...]}. Off by default.",
			},
		},
		Results: []BuiltinParameter{
			{
				Name:        "status_code",
				Type:        "number",
				Required:    true,
				Description: "The HTTP response status code.",
			},
			{
				Name:        "headers",
				Type:        "object of strings",
				Required:    true,
				Description: "The response headers.",
			},
			{
				Name:        "body",
				Type:        "any or string",
				Required:    true,
				Description: "The response body: parsed JSON in the json format, a plain string in the text format.",
			},
			{
				Name:        "content_type",
				Type:        "string",
				Required:    true,
				Description: "The response content type.",
			},
			{
				Name:        "duration_ms",
				Type:        "number",
				Required:    true,
				Description: "How long the request took, in milliseconds.",
			},
		},
		Constraints: []string{
			"Refuses loopback, private, link-local, and cloud-metadata addresses, so a request cannot be aimed at the host or an internal service.",
			"Does not follow redirects unless asked to; when it does, every hop is checked against the same address policy.",
			"Does not retry unless asked to, and never retries a method that was not opted in.",
			"Bounds the request body and the response body.",
		},
	},
	{
		Name:    "command",
		Summary: "Runs a local program",
		Description: "command executes a local program with structured arguments and returns its exit code, " +
			"standard output, and standard error. It is the boundary between an assembly and the programs " +
			"already installed on the machine.",
		Parameters: []BuiltinParameter{
			{
				Name:        "command",
				Type:        "string",
				Required:    true,
				Description: "The program to run. A single executable, never a shell command line.",
			},
			{
				Name:        "args",
				Type:        "array of strings",
				Description: "The program's arguments.",
			},
			{
				Name:        "env",
				Type:        "object of strings",
				Description: "Environment variables added to the program's environment.",
			},
			{
				Name:        "cwd",
				Type:        "string",
				Description: "The working directory, resolved against the allowed root.",
			},
			{
				Name:        "stdin",
				Type:        "string",
				Description: "Text written to the program's standard input.",
			},
			{
				Name:        "timeout",
				Type:        "duration string",
				Description: "How long the program may run, such as \"30s\".",
			},
			{
				Name:        "format",
				Type:        "string",
				Default:     "text",
				Description: "How standard output is represented: \"json\" or \"text\".",
			},
			{
				Name:        "failOnNonZeroExit",
				Type:        "boolean",
				Default:     "true",
				Description: "Whether a non-zero exit fails the capability. Set it to false to inspect the result instead.",
			},
			{
				Name:        "maxOutputBytes",
				Type:        "number",
				Description: "The most output to retain per stream. The runtime's ceiling applies when this is absent.",
			},
		},
		Results: []BuiltinParameter{
			{
				Name:        "exit_code",
				Type:        "number",
				Required:    true,
				Description: "The program's exit code.",
			},
			{
				Name:        "stdout",
				Type:        "any or string",
				Required:    true,
				Description: "The program's standard output: parsed JSON in the json format, a plain string in the text format.",
			},
			{
				Name:        "stderr",
				Type:        "string",
				Required:    true,
				Description: "The program's standard error.",
			},
			{
				Name:        "success",
				Type:        "boolean",
				Required:    true,
				Description: "Whether the program exited with code zero.",
			},
			{
				Name:        "timed_out",
				Type:        "boolean",
				Required:    true,
				Description: "Whether the program was terminated by its deadline.",
			},
			{
				Name:        "duration_ms",
				Type:        "number",
				Required:    true,
				Description: "How long the program ran, in milliseconds.",
			},
		},
		Constraints: []string{
			"Never runs a shell: the program and its arguments are passed to the operating system separately, so an argument can never become a command.",
			"Runs only the programs the policy permits, when the policy names any.",
			"Runs the program with an allowlisted environment, never the runtime's own environment.",
			"Confines the working directory to an allowed root.",
			"Bounds the retained output and reports truncation rather than hiding it.",
			"Terminates the program's whole process group on cancellation or deadline, so no child survives the capability.",
		},
	},
	{
		Name:    "filesystem",
		Summary: "Reads files and directories",
		Description: "filesystem reads a file, lists a directory, and reports what a path is. It is the " +
			"read-only view of the machine the assembly runs on: the files the assembly was written to work " +
			"with, and nothing else. Writing, deleting, and moving files are not part of it.",
		Parameters: []BuiltinParameter{
			{
				Name:        "operation",
				Type:        "string",
				Required:    true,
				Description: "What to do with the path: \"read\", \"list\", or \"stat\".",
			},
			{
				Name:        "path",
				Type:        "string",
				Required:    true,
				Description: "The path to read, list, or describe. A relative path is resolved against the project root.",
			},
			{
				Name:        "recursive",
				Type:        "boolean",
				Default:     "false",
				Description: "Whether a listing descends into subdirectories.",
			},
			{
				Name:        "format",
				Type:        "string",
				Default:     "json",
				Description: "How the content of a read file is represented: \"json\" or \"text\".",
			},
		},
		Results: []BuiltinParameter{
			{
				Name:        "path",
				Type:        "string",
				Required:    true,
				Description: "The absolute path the operation applied to.",
			},
			{
				Name:        "content",
				Type:        "any or string",
				Description: "The content of the file, for the read operation: parsed JSON in the json format, a plain string in the text format.",
			},
			{
				Name:        "size",
				Type:        "number",
				Description: "The size of the file in bytes, for the read and stat operations.",
			},
			{
				Name:        "entries",
				Type:        "array of objects",
				Description: "The entries of the directory, for the list operation.",
			},
			{
				Name:        "exists",
				Type:        "boolean",
				Description: "Whether the path exists, for the stat operation.",
			},
			{
				Name:        "type",
				Type:        "string",
				Description: "What the path is: \"file\", \"directory\", \"link\", or \"other\".",
			},
		},
		Constraints: []string{
			"Refuses a path that resolves outside the permitted roots, and a symbolic link cannot be used to leave them.",
			"Reads only: no operation writes, deletes, or moves anything.",
			"Bounds the content a single read returns and the entries a single listing returns.",
		},
	},
}

// BuiltinDescriptors returns the authoritative list of built-in capability
// runtimes.
//
// The returned slice is a copy: a caller that mutates it cannot change what
// the runtime registers.
func BuiltinDescriptors() []BuiltinDescriptor {
	descriptors := make([]BuiltinDescriptor, len(builtinDescriptors))
	copy(descriptors, builtinDescriptors)
	return descriptors
}

// LookupBuiltin returns the descriptor of the built-in named by t.
//
// t may be the canonical namespaced form (neuron:core:http) or the legacy
// bare form (http). The second result reports whether a built-in was found.
func LookupBuiltin(t CapabilityRuntimeType) (BuiltinDescriptor, bool) {
	name := builtinName(t)
	if name == "" {
		return BuiltinDescriptor{}, false
	}
	for _, descriptor := range builtinDescriptors {
		if descriptor.Name == name {
			return descriptor, true
		}
	}
	return BuiltinDescriptor{}, false
}

// IsBuiltinRuntimeType reports whether t names a built-in capability runtime
// that exists.
//
// This is a different question from IsCoreRuntimeType. The core namespace
// answers "is this capability runtime provided in-process by the runtime,
// and therefore never resolved or installed"; this answers "is there a
// built-in with this name". A name in the core namespace that is not a
// built-in is a reference to something that does not exist, and it must fail
// where the reference is made rather than where the capability runs.
func IsBuiltinRuntimeType(t CapabilityRuntimeType) bool {
	_, ok := LookupBuiltin(t)
	return ok
}

// BuiltinTypeNames returns the names of every built-in capability runtime,
// sorted.
//
// The names are the bare forms. The canonical namespaced form of a built-in
// is neuron:core:<name>.
func BuiltinTypeNames() []string {
	names := make([]string, 0, len(builtinDescriptors))
	for _, descriptor := range builtinDescriptors {
		names = append(names, descriptor.Name)
	}
	sort.Strings(names)
	return names
}

// BuiltinTypes returns the canonical type of every built-in capability
// runtime, sorted.
//
// The canonical type is the name an assembly refers to a built-in by.
func BuiltinTypes() []CapabilityRuntimeType {
	names := BuiltinTypeNames()
	types := make([]CapabilityRuntimeType, 0, len(names))
	for _, name := range names {
		types = append(types, CoreName(name))
	}
	return types
}

// builtinTypeNames returns the canonical type of every built-in as text, for
// an error message that names the built-ins an author may refer to.
func builtinTypeNames() []string {
	types := BuiltinTypes()
	names := make([]string, 0, len(types))
	for _, builtinType := range types {
		names = append(names, string(builtinType))
	}
	return names
}

// builtinName extracts the built-in name from a capability runtime type.
//
// It accepts the canonical namespaced form and the legacy bare form, and
// returns an empty string for anything else, including a name in the core
// namespace that is not a built-in.
func builtinName(t CapabilityRuntimeType) string {
	s := string(t)
	if s == "" {
		return ""
	}
	parts := strings.Split(s, ":")
	if len(parts) == 3 && parts[0] == CoreOwner && parts[1] == CoreNamespace {
		return parts[2]
	}
	if len(parts) == 1 {
		return s
	}
	return ""
}

// RemovedBuiltin records a built-in capability runtime that no longer exists.
//
// The record exists for exactly one reason. An assembly authored before the
// removal still references the name, and a reference to something that no
// longer exists has to fail with an explanation of what happened and what to
// use instead — not with a bare "not found". Nothing is registered, resolved,
// or executed under these names.
type RemovedBuiltin struct {
	// Name is the built-in's former name within the core namespace.
	Name string

	// Reason explains why the built-in was removed.
	Reason string

	// Replacement names what supersedes the built-in.
	Replacement string
}

// removedBuiltins records the built-in capability runtimes that were removed,
// with the reason each was removed and what supersedes it.
var removedBuiltins = []RemovedBuiltin{
	{
		Name:   "ai",
		Reason: "it returned a canned string rather than calling a provider, so it was a mock rather than a capability",
		Replacement: "a capability runtime published for the AI provider you use, referenced from the " +
			"assembly like any other external capability runtime",
	},
	{
		Name:        "delay",
		Reason:      "it only waited, which is a sequencing concern rather than a capability",
		Replacement: "an explicit dependency between the capabilities that must run in order",
	},
	{
		Name:        "log",
		Reason:      "the runtime already records what a capability received, so the built-in only duplicated it",
		Replacement: "the runtime's own execution logs and the capability's stderr",
	},
}

// RemovedBuiltins returns the built-in capability runtimes that were removed.
func RemovedBuiltins() []RemovedBuiltin {
	removed := make([]RemovedBuiltin, len(removedBuiltins))
	copy(removed, removedBuiltins)
	return removed
}

// LookupRemovedBuiltin returns the removal record of the built-in named by t.
//
// t may be the canonical namespaced form or the legacy bare form. The second
// result reports whether a removal was found.
func LookupRemovedBuiltin(t CapabilityRuntimeType) (RemovedBuiltin, bool) {
	name := builtinName(t)
	if name == "" {
		return RemovedBuiltin{}, false
	}
	for _, removed := range removedBuiltins {
		if removed.Name == name {
			return removed, true
		}
	}
	return RemovedBuiltin{}, false
}

// IsRemovedBuiltinType reports whether t names a built-in capability runtime
// that was removed.
func IsRemovedBuiltinType(t CapabilityRuntimeType) bool {
	_, ok := LookupRemovedBuiltin(t)
	return ok
}

// UnknownBuiltinError describes a reference to a built-in capability runtime
// that does not exist.
//
// The error names the available built-ins so the author can correct the
// reference without consulting the documentation, and it explains a removal
// when the reference names a built-in that used to exist.
//
// It returns nil for a reference to a built-in that exists, and for a
// reference that names an external capability runtime package, because such a
// reference is resolved by the registry instead.
func UnknownBuiltinError(t CapabilityRuntimeType) error {
	if IsBuiltinRuntimeType(t) {
		return nil
	}

	available := "available built-in capability runtimes are: " + strings.Join(builtinTypeNames(), ", ")

	if removed, wasRemoved := LookupRemovedBuiltin(t); wasRemoved {
		return fmt.Errorf("capability runtime %q does not exist: it was removed because %s; use %s instead. %s",
			t, removed.Reason, removed.Replacement, available)
	}
	if IsCoreNamespaceType(t) {
		return fmt.Errorf("capability runtime %q does not exist. %s", t, available)
	}
	return nil
}

// IsCoreNamespaceType reports whether t names the core namespace, whether or
// not a built-in with that name exists.
//
// It answers "was this meant to be a built-in?", which is a different
// question from IsBuiltinRuntimeType. A name in the core namespace that is
// not a built-in is a mistake in the assembly, and it must be reported as
// such rather than being sent to the package registry.
func IsCoreNamespaceType(t CapabilityRuntimeType) bool {
	parts := strings.Split(string(t), ":")
	return len(parts) == 3 && parts[0] == CoreOwner && parts[1] == CoreNamespace
}
