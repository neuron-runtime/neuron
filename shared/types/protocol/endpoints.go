package protocol

import (
	"net/url"
	"regexp"
)

// The route table for the N.O.R.E. local API.
//
// These templates are the single description of every endpoint in this file, and
// they are written in the named-wildcard form Go's ServeMux registers, because
// the server is what ultimately has to serve them. The client cannot use the
// templates directly -- it needs a concrete URL -- so every parameterized
// template has a constructor below that substitutes the placeholders and escapes
// the values as path segments.
//
// Both directions go through this one table on purpose. A client that
// hand-assembled a path, or a server that registered a literal, would agree with
// the other side only until one of them changed: nothing in the build would fail,
// and the mismatch would surface as a 404 or, worse, a silently dropped field.
const (
	HealthPath                = "/health"
	InstancesPath             = "/v1/instances"
	InstanceByIDPath          = "/v1/instances/{id}"
	ExecutePath               = "/v1/instances/{id}/executions"
	ExecutionByIDPath         = "/v1/instances/{id}/executions/{execID}"
	ExecutionEventsPath       = "/v1/instances/{id}/executions/{execID}/events"
	ExecutionEventsStreamPath = "/v1/instances/{id}/executions/{execID}/events/stream"
	CancelExecutionPath       = "/v1/instances/{id}/executions/{execID}/cancel"

	WebSocketPath = "/v1/ws"

	RegisterPath = "/v1/register"
)

// InstanceByID returns the path addressing a single instance.
func InstanceByID(instanceID string) string {
	return fillRoute(InstanceByIDPath, instanceID)
}

// InstanceExecutions returns the path addressing the execution collection of one
// instance.
func InstanceExecutions(instanceID string) string {
	return fillRoute(ExecutePath, instanceID)
}

// ExecutionByID returns the path addressing a single execution.
func ExecutionByID(instanceID, executionID string) string {
	return fillRoute(ExecutionByIDPath, instanceID, executionID)
}

// ExecutionEvents returns the path addressing the persisted events of one
// execution.
func ExecutionEvents(instanceID, executionID string) string {
	return fillRoute(ExecutionEventsPath, instanceID, executionID)
}

// ExecutionEventsStream returns the path addressing the live event stream of one
// execution.
func ExecutionEventsStream(instanceID, executionID string) string {
	return fillRoute(ExecutionEventsStreamPath, instanceID, executionID)
}

// CancelExecution returns the path addressing the cancellation of one execution.
func CancelExecution(instanceID, executionID string) string {
	return fillRoute(CancelExecutionPath, instanceID, executionID)
}

// routePlaceholder matches a named wildcard in a route template. It is
// restricted to identifier-shaped names so that a literal brace in a future
// constant is not silently treated as a placeholder.
var routePlaceholder = regexp.MustCompile(`\{[a-zA-Z][a-zA-Z0-9]*\}`)

// fillRoute renders a route template by substituting its named placeholders, in
// order, with the given values escaped as single URL path segments.
//
// Escaping belongs here rather than at the call sites because the two halves have
// to happen together. A caller that formats a template by hand can escape one
// argument and forget another, and the codebase had already done exactly that:
// some execution paths escaped their IDs and others passed them through, so the
// same identifier was addressed inconsistently depending on the route.
//
// Substitution walks the template once instead of replacing placeholders in
// sequence. Repeated replacement would re-examine text a previous step had
// already substituted, so a value that managed to look like a placeholder could
// be expanded a second time.
func fillRoute(template string, values ...string) string {
	if len(values) == 0 {
		return template
	}
	next := 0
	return routePlaceholder.ReplaceAllStringFunc(template, func(name string) string {
		if next >= len(values) {
			// Leaving the placeholder in place makes a mismatched call visible in
			// the resulting path, instead of silently dropping the segment and
			// addressing a different resource than the caller named.
			return name
		}
		value := url.PathEscape(values[next])
		next++
		return value
	})
}

// Method joins an HTTP method to a route template, producing the pattern Go's
// ServeMux registers.
//
// It exists so a route's method and path are written as one expression. The space
// between them is part of ServeMux's pattern syntax rather than an accident, and
// spelling it out at every call site is how a method quietly ends up applied to
// the wrong path.
func Method(method, template string) string {
	return method + " " + template
}
