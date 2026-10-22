package acceptance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// `neuron run` discovers the runtime from any directory inside the project, not
// only from the project root.
func TestRunFromSubdirectory(t *testing.T) {
	p := writeProject(t)
	buildProject(t, p.root)
	startRuntime(t, p.root)

	nested := filepath.Join(p.root, "checkout", "terminal")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	result := runNeuron(t, nested, nil, "run", "--params", `{"name":"Ada"}`)
	if result.code != 0 {
		t.Fatalf("neuron run from a subdirectory exit = %d\nstdout: %s\nstderr: %s", result.code, result.stdout, result.stderr)
	}
	if got := decodeSingleJSON(t, result.stdout)["caller"]; got != "Ada" {
		t.Errorf("caller = %v, want Ada", got)
	}
}

// `neuron run --port` targets the local runtime even when it publishes a
// discovery record for automatic discovery.
func TestRunWithPortOverride(t *testing.T) {
	p := writeProject(t)
	buildProject(t, p.root)
	session := startRuntime(t, p.root)

	result := runNeuron(t, p.root, nil, "run", "--port", session.record.Port, p.assembly, "--params", `{"name":"Ada"}`)
	if result.code != 0 {
		t.Fatalf("neuron run --port exit = %d\nstdout: %s\nstderr: %s", result.code, result.stdout, result.stderr)
	}
	if got := decodeSingleJSON(t, result.stdout)["caller"]; got != "Ada" {
		t.Errorf("caller = %v, want Ada", got)
	}
}

// `neuron run --remote` addresses a runtime by URL from outside the project,
// authenticating with the token the discovery record publishes.
func TestRunWithRemoteEndpoint(t *testing.T) {
	p := writeProject(t)
	buildProject(t, p.root)
	session := startRuntime(t, p.root)

	outside := t.TempDir()
	result := runNeuron(t, outside,
		[]string{"NEURON_API_TOKEN=" + session.record.Token},
		"run", "--remote", session.record.Endpoint(), p.assembly, "--params", `{"name":"Ada"}`,
	)
	if result.code != 0 {
		t.Fatalf("neuron run --remote exit = %d\nstdout: %s\nstderr: %s", result.code, result.stdout, result.stderr)
	}
	if got := decodeSingleJSON(t, result.stdout)["caller"]; got != "Ada" {
		t.Errorf("caller = %v, want Ada", got)
	}
}

// `neuron run --params` returns the Assembly result on stdout and nothing else;
// the result is assembled from two different capabilities plus an Assembly
// parameter.
func TestRunWithParamsReturnsAssemblyResult(t *testing.T) {
	p := writeProject(t)
	buildProject(t, p.root)
	startRuntime(t, p.root)

	result := runNeuron(t, p.root, nil, "run", p.assembly, "--params", `{"name":"Ada"}`)
	if result.code != 0 {
		t.Fatalf("neuron run exit = %d\nstdout: %s\nstderr: %s", result.code, result.stdout, result.stderr)
	}

	want := map[string]any{
		"greeting": "hello",
		"echoed":   "hello",
		"mark":     "!",
		"caller":   "Ada",
	}
	if got := decodeSingleJSON(t, result.stdout); !reflect.DeepEqual(got, want) {
		t.Errorf("result = %#v, want %#v", got, want)
	}
}

// `neuron run --params-file` is equivalent to `--params` without shell quoting.
func TestRunWithParamsFile(t *testing.T) {
	p := writeProject(t)
	buildProject(t, p.root)
	startRuntime(t, p.root)

	paramsPath := filepath.Join(t.TempDir(), "params.json")
	if err := os.WriteFile(paramsPath, []byte(`{"name":"Grace"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	result := runNeuron(t, p.root, nil, "run", p.assembly, "--params-file", paramsPath)
	if result.code != 0 {
		t.Fatalf("neuron run --params-file exit = %d\nstdout: %s\nstderr: %s", result.code, result.stdout, result.stderr)
	}
	if got := decodeSingleJSON(t, result.stdout)["caller"]; got != "Grace" {
		t.Errorf("caller = %v, want Grace", got)
	}
}

// A failed execution must exit non-zero, keep stdout empty, and explain the
// failure on stderr.
func TestFailedExecutionExitsNonZeroWithDiagnostics(t *testing.T) {
	p := writeProject(t)
	buildProject(t, p.root)
	startRuntime(t, p.root)

	result := runNeuron(t, p.root, nil, "run", p.failing, "--params", `{"name":"Ada"}`)
	if result.code == 0 {
		t.Fatalf("failed execution exit = 0, want non-zero\nstdout: %s", result.stdout)
	}
	if strings.TrimSpace(result.stdout) != "" {
		t.Errorf("stdout is not empty on failure: %s", result.stdout)
	}
	if strings.TrimSpace(result.stderr) == "" {
		t.Error("stderr is empty on failure, want a diagnostic")
	}
	if !strings.Contains(result.stderr, "execution failed") {
		t.Errorf("stderr does not describe the execution failure: %s", result.stderr)
	}
}

// The canonical manifest persisted by the build carries structured references,
// never the legacy mapping-expression strings.
func TestCanonicalManifestUsesStructuredReferences(t *testing.T) {
	p := writeProject(t)
	buildProject(t, p.root)

	path := filepath.Join(p.root, ".neuron", "assemblies", p.assembly, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var manifest struct {
		Bindings []struct {
			Mappings []map[string]any `json:"mappings"`
		} `json:"bindings"`
		Result *struct {
			Fields []struct {
				Name   string         `json:"name"`
				Source map[string]any `json:"source"`
			} `json:"fields"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode canonical manifest: %v", err)
	}

	if len(manifest.Bindings) != 1 || len(manifest.Bindings[0].Mappings) != 1 {
		t.Fatalf("canonical manifest bindings = %#v, want one binding with one mapping", manifest.Bindings)
	}
	mapping := manifest.Bindings[0].Mappings[0]
	if _, ok := mapping["expression"]; ok {
		t.Errorf("canonical mapping still carries an expression string: %#v", mapping)
	}
	source, ok := mapping["source"].(map[string]any)
	if !ok {
		t.Fatalf("canonical mapping has no structured source: %#v", mapping)
	}
	if source["kind"] != "capabilityResult" || source["capability"] != "greet" {
		t.Errorf("canonical mapping source = %#v, want the greet capability result", source)
	}

	if manifest.Result == nil || len(manifest.Result.Fields) != 4 {
		t.Fatalf("canonical result = %#v, want four structured fields", manifest.Result)
	}
	for _, field := range manifest.Result.Fields {
		if field.Source == nil {
			t.Errorf("result field %q has no structured source", field.Name)
			continue
		}
		if _, ok := field.Source["kind"]; !ok {
			t.Errorf("result field %q source has no kind: %#v", field.Name, field.Source)
		}
	}
}

// Building the same sources twice must produce the same identity and the same
// canonical manifest, proving hashing is deterministic.
func TestBuildIsDeterministic(t *testing.T) {
	p := writeProject(t)

	first := runNeuron(t, p.root, nil, "build")
	if first.code != 0 {
		t.Fatalf("first build exit = %d\nstdout: %s\nstderr: %s", first.code, first.stdout, first.stderr)
	}
	firstManifest, err := os.ReadFile(filepath.Join(p.root, ".neuron", "assemblies", p.assembly, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}

	second := runNeuron(t, p.root, nil, "build")
	if second.code != 0 {
		t.Fatalf("second build exit = %d\nstdout: %s\nstderr: %s", second.code, second.stdout, second.stderr)
	}
	secondManifest, err := os.ReadFile(filepath.Join(p.root, ".neuron", "assemblies", p.assembly, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}

	if string(firstManifest) != string(secondManifest) {
		t.Error("canonical manifest changed between identical builds")
	}
	if registrationKey(first.stdout) != registrationKey(second.stdout) {
		t.Errorf("registration identity changed between identical builds:\nfirst: %s\nsecond: %s", first.stdout, second.stdout)
	}
	if registrationKey(first.stdout) == "" {
		t.Errorf("build printed no registration record:\n%s", first.stdout)
	}
}

// registrationKey strips the parenthesised status from a registration line.
func registrationKey(stdout string) string {
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}
		if idx := strings.Index(line, " ("); idx >= 0 {
			return line[:idx]
		}
		return line
	}
	return ""
}
