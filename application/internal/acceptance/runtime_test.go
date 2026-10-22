package acceptance

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/shared/types/projectruntime"
)

// Two projects started at the same time must each get their own runtime
// process, endpoint, and discovery record.
func TestConcurrentProjectsHaveIndependentRuntimes(t *testing.T) {
	first := writeProject(t)
	second := writeProject(t)
	buildProject(t, first.root)
	buildProject(t, second.root)

	firstRuntime := startRuntime(t, first.root)
	secondRuntime := startRuntime(t, second.root)

	if firstRuntime.record.Port == secondRuntime.record.Port {
		t.Fatalf("both runtimes bound port %s; each project needs an independent endpoint", firstRuntime.record.Port)
	}
	if firstRuntime.record.ProjectRoot != first.root {
		t.Errorf("first runtime root = %q, want %q", firstRuntime.record.ProjectRoot, first.root)
	}
	if secondRuntime.record.ProjectRoot != second.root {
		t.Errorf("second runtime root = %q, want %q", secondRuntime.record.ProjectRoot, second.root)
	}
	for name, session := range map[string]*runtimeSession{"first": firstRuntime, "second": secondRuntime} {
		if err := health(session.record); err != nil {
			t.Errorf("%s runtime health: %v", name, err)
		}
		if !strings.Contains(session.log(), "Neuron project runtime started on http://") {
			t.Errorf("%s runtime did not report a canonical URL\n%s", name, session.log())
		}
	}
}

// `runtime.port: auto` must report the port the OS actually assigned.
func TestAutoPortIsReported(t *testing.T) {
	p := writeProject(t)
	buildProject(t, p.root)

	session := startRuntime(t, p.root)

	port, err := strconv.Atoi(session.record.Port)
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("discovery record port = %q, want a bound TCP port", session.record.Port)
	}
	if !strings.Contains(session.log(), "started on http://localhost:"+session.record.Port) {
		t.Errorf("startup output does not report the bound port %s\n%s", session.record.Port, session.log())
	}
}

// `neuron --port` overrides `runtime.port` and the runtime binds the requested
// port.
func TestExplicitPortFlagIsUsed(t *testing.T) {
	p := writeProject(t)
	buildProject(t, p.root)

	wanted := freePort(t)
	session := startRuntime(t, p.root, "--port", wanted)

	if session.record.Port != wanted {
		t.Fatalf("discovery record port = %q, want requested %q", session.record.Port, wanted)
	}
	if !strings.Contains(session.log(), "started on http://localhost:"+wanted) {
		t.Errorf("startup output does not report the requested port %s\n%s", wanted, session.log())
	}
}

// A second foreground runtime for the same project is a second session: it must
// not overwrite or remove the first session's discovery metadata, and both
// endpoints must remain live.
func TestSecondRuntimeForSameProjectCoexists(t *testing.T) {
	p := writeProject(t)
	buildProject(t, p.root)

	first := startRuntime(t, p.root)
	second := startRuntime(t, p.root)

	if first.record.SessionID == second.record.SessionID {
		t.Fatal("both sessions published the same session id")
	}
	if first.record.Port == second.record.Port {
		t.Fatal("second session reused the first session's endpoint")
	}

	records, err := projectruntime.ReadAll(p.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("project has %d discovery records, want 2 (one per session)", len(records))
	}
	for _, record := range records {
		if err := health(record); err != nil {
			t.Errorf("session %s at %s is not healthy: %v", record.SessionID, record.Endpoint(), err)
		}
	}

	result := runNeuron(t, p.root, nil, "run", p.assembly, "--params", `{"name":"Ada"}`)
	if result.code != 0 {
		t.Fatalf("neuron run exit = %d\nstdout: %s\nstderr: %s", result.code, result.stdout, result.stderr)
	}

	after, err := projectruntime.ReadAll(p.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 {
		t.Fatalf("a run removed discovery metadata; %d sessions remain, want 2", len(after))
	}
}

// One foreground runtime serves every Assembly declared and built by the
// project, each with its own result contract.
func TestMultipleAssembliesServeFromOneRuntime(t *testing.T) {
	p := writeProject(t)
	buildProject(t, p.root)
	session := startRuntime(t, p.root)

	first := runNeuron(t, p.root, nil, "run", p.assembly, "--params", `{"name":"Ada"}`)
	if first.code != 0 {
		t.Fatalf("run %s exit = %d\nstdout: %s\nstderr: %s", p.assembly, first.code, first.stdout, first.stderr)
	}
	second := runNeuron(t, p.root, nil, "run", p.alt, "--params", `{"name":"Ada"}`)
	if second.code != 0 {
		t.Fatalf("run %s exit = %d\nstdout: %s\nstderr: %s", p.alt, second.code, second.stdout, second.stderr)
	}

	full := decodeSingleJSON(t, first.stdout)
	if full["echoed"] != "hello" || full["caller"] != "Ada" {
		t.Errorf("first Assembly result = %#v", full)
	}
	alt := decodeSingleJSON(t, second.stdout)
	want := map[string]any{"greeting": "hello", "caller": "Ada"}
	if !reflect.DeepEqual(alt, want) {
		t.Errorf("second Assembly result = %#v, want %#v", alt, want)
	}

	if err := health(session.record); err != nil {
		t.Errorf("runtime is no longer healthy after serving both Assemblies: %v", err)
	}
}
