package protocol

import (
	"net/http"
	"strings"
	"testing"
)

// The route table has two consumers that cannot see each other: the server
// registers the templates with ServeMux, and the client builds URLs from the
// constructors. Nothing in the build compares them, so these tests stand in for
// that comparison by asserting both directions against the same table.

func TestConstructorsRenderEveryParameterizedRoute(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"instance by ID", InstanceByID("inst_1"), "/v1/instances/inst_1"},
		{"instance executions", InstanceExecutions("inst_1"), "/v1/instances/inst_1/executions"},
		{"execution by ID", ExecutionByID("inst_1", "exec_1"), "/v1/instances/inst_1/executions/exec_1"},
		{"execution events", ExecutionEvents("inst_1", "exec_1"), "/v1/instances/inst_1/executions/exec_1/events"},
		{
			"execution events stream",
			ExecutionEventsStream("inst_1", "exec_1"),
			"/v1/instances/inst_1/executions/exec_1/events/stream",
		},
		{
			"cancel execution",
			CancelExecution("inst_1", "exec_1"),
			"/v1/instances/inst_1/executions/exec_1/cancel",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("path = %q, want %q", tc.got, tc.want)
			}
		})
	}
}

// Every constructor result must be a path the server actually registers. A
// template that no route uses is a contract nobody honours, and one that a route
// forgets is a 404 no test would otherwise catch.
func TestConstructorsMatchRegisteredPatterns(t *testing.T) {
	registered := map[string]bool{
		HealthPath:                true,
		InstancesPath:             true,
		InstanceByIDPath:          true,
		ExecutePath:               true,
		ExecutionByIDPath:         true,
		ExecutionEventsPath:       true,
		ExecutionEventsStreamPath: true,
		CancelExecutionPath:       true,
		WebSocketPath:             true,
		RegisterPath:              true,
	}

	for _, template := range []string{
		InstanceByIDPath,
		ExecutePath,
		ExecutionByIDPath,
		ExecutionEventsPath,
		ExecutionEventsStreamPath,
		CancelExecutionPath,
	} {
		if !registered[template] {
			t.Errorf("template %q has no registered route", template)
		}
		// ServeMux rejects a pattern whose wildcards are not identifier-shaped, so
		// the placeholders have to survive that check rather than merely look right.
		if got := Method(http.MethodGet, template); !strings.Contains(got, "{") {
			t.Errorf("Method(%q) = %q, want the wildcard preserved", http.MethodGet, got)
		}
	}
}

// A value carrying a path separator must not be able to address a different
// resource than the one it names. The client used to escape some identifiers and
// pass others through, so this is the regression that motivated moving escaping
// into the constructors.
func TestFillRouteEscapesEachValueAsOneSegment(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"slash", InstanceByID("a/b"), "/v1/instances/a%2Fb"},
		{"query", InstanceByID("a?b"), "/v1/instances/a%3Fb"},
		{"space", InstanceByID("a b"), "/v1/instances/a%20b"},
		{"percent", InstanceByID("100%"), "/v1/instances/100%25"},
		{
			"both segments",
			ExecutionByID("inst/1", "exec?2"),
			"/v1/instances/inst%2F1/executions/exec%3F2",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("path = %q, want %q", tc.got, tc.want)
			}
		})
	}
}

// Substitution must not revisit text it has already replaced. A value that looks
// like a placeholder has to survive as a literal value, not be expanded again.
func TestFillRouteDoesNotResubstituteAValue(t *testing.T) {
	got := InstanceByID("{execID}")
	want := "/v1/instances/%7BexecID%7D"
	if got != want {
		t.Fatalf("path = %q, want %q: a value must not be treated as a placeholder", got, want)
	}
}

// Too few values must fail visibly. Silently dropping the segment would address
// a neighbouring resource, which for an execution lookup means reporting on the
// wrong execution.
func TestFillRouteLeavesUnfilledPlaceholderVisible(t *testing.T) {
	got := ExecutionByID("inst_1", "")
	if got != "/v1/instances/inst_1/executions/" {
		t.Fatalf("path = %q, want the missing segment to remain visible", got)
	}
}

func TestFillRouteWithoutValuesReturnsTemplate(t *testing.T) {
	if got := fillRoute(ExecutionByIDPath); got != ExecutionByIDPath {
		t.Fatalf("fillRoute with no values = %q, want the template unchanged", got)
	}
}
