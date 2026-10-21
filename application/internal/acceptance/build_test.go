package acceptance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// overwriteFailingAssembly replaces the fixture's failing Assembly with content
// that must be rejected during the build.
func overwriteFailingAssembly(t *testing.T, p project, content string) {
	t.Helper()
	writeFile(t, filepath.Join(p.root, "assemblies", p.failing, "assembly.yaml"), content)
}

// resultManifestYAML is an Assembly whose result maps one capability result.
func resultManifestYAML(greetEntry, capability, path string) string {
	return `apiVersion: neuron/v1
kind: Assembly

capabilities:
  - ref: greet
    entry: ` + greetEntry + `
  - ref: punctuate
    entry: ../../capabilities/punctuate.yaml

bindings:
  - from: greet
    to: punctuate
    mappings:
      - target: text
        expression: "source.result.greeting"

result:
  schema:
    - name: greeting
      type: string
  fields:
    - name: greeting
      source:
        kind: capabilityResult
        capability: ` + capability + `
        path: [` + path + `]
`
}

const detachedGreetCapability = `apiVersion: neuron/v1
kind: Capability

metadata:
  name: greet
  version: 0.1.0

spec:
  capability runtime:
    type: neuron:core:set
    runtimeConfig:
      execution:
        mode: detach

  config:
    greeting: hello

  mappings:
    - direction: output
      source: greeting
      target: greeting
`

// A result field must name a declared capability result port; a typo fails the
// build instead of surfacing at execution time.
func TestInvalidCapabilityResultReferenceFailsBuild(t *testing.T) {
	p := writeProject(t)
	overwriteFailingAssembly(t, p, resultManifestYAML("../../capabilities/greet.yaml", "greet", "missing"))

	res := runNeuron(t, p.root, nil, "build")
	if res.code == 0 {
		t.Fatalf("build succeeded with an unknown result port\nstdout: %s", res.stdout)
	}
	if !strings.Contains(res.stderr, `declares no result port matching "missing"`) {
		t.Errorf("stderr does not explain the invalid result reference: %s", res.stderr)
	}
}

// A detached capability runs in a child execution that may outlive the
// Assembly, so its result cannot feed the Assembly result.
func TestDetachedCapabilityResultFailsBuild(t *testing.T) {
	p := writeProject(t)
	writeFile(t, filepath.Join(p.root, "capabilities", "detached-greet.yaml"), detachedGreetCapability)
	overwriteFailingAssembly(t, p, resultManifestYAML("../../capabilities/detached-greet.yaml", "greet", "greeting"))

	res := runNeuron(t, p.root, nil, "build")
	if res.code == 0 {
		t.Fatalf("build succeeded with a detached capability result\nstdout: %s", res.stdout)
	}
	if !strings.Contains(res.stderr, "detached branch") {
		t.Errorf("stderr does not explain the detached result reference: %s", res.stderr)
	}
}

// Everything downstream of a detached capability belongs to the same child
// execution, so a descendant result is equally unavailable.
func TestDescendantOfDetachedCapabilityResultFailsBuild(t *testing.T) {
	p := writeProject(t)
	writeFile(t, filepath.Join(p.root, "capabilities", "detached-greet.yaml"), detachedGreetCapability)
	overwriteFailingAssembly(t, p, resultManifestYAML("../../capabilities/detached-greet.yaml", "punctuate", "text"))

	res := runNeuron(t, p.root, nil, "build")
	if res.code == 0 {
		t.Fatalf("build succeeded with a descendant of a detached capability result\nstdout: %s", res.stdout)
	}
	if !strings.Contains(res.stderr, "detached branch owned by") {
		t.Errorf("stderr does not explain the descendant result reference: %s", res.stderr)
	}
}

const branchCapability = `apiVersion: neuron/v1
kind: Capability

metadata:
  name: branch
  version: 0.1.0

spec:
  capability runtime:
    type: neuron:core:set

  config:
    token: branch

  mappings:
    - direction: output
      source: token
      target: token
`

const branchedAssemblyYAML = `apiVersion: neuron/v1
kind: Assembly

capabilities:
  - ref: greet
    entry: ../../capabilities/greet.yaml
  - ref: punctuate
    entry: ../../capabilities/punctuate.yaml
  - ref: branch
    entry: ../../capabilities/branch.yaml

bindings:
  - from: greet
    to: punctuate
    mappings:
      - target: text
        expression: "source.result.greeting"
    validations:
      - expression: "source.result.greeting != ''"
        message: "greeting validation failed"
  - from: greet
    to: branch
    mappings:
      - target: token
        expression: "source.result.greeting"

result:
  schema:
    - name: greeting
      type: string
    - name: echoed
      type: string
    - name: branch
      type: string
  fields:
    - name: greeting
      source:
        kind: capabilityResult
        capability: greet
        path: [greeting]
    - name: echoed
      source:
        kind: capabilityResult
        capability: punctuate
        path: [text]
    - name: branch
      source:
        kind: capabilityResult
        capability: branch
        path: [token]
`

// Independent non-detached branches may each contribute to the Assembly
// result; neither branch blocks the other from being resolved.
func TestIndependentBranchesContributeToResult(t *testing.T) {
	p := writeProject(t)
	writeFile(t, filepath.Join(p.root, "capabilities", "branch.yaml"), branchCapability)
	writeFile(t, filepath.Join(p.root, "assemblies", p.assembly, "assembly.yaml"), branchedAssemblyYAML)
	buildProject(t, p.root)
	startRuntime(t, p.root)

	result := runNeuron(t, p.root, nil, "run", "--params", `{"name":"World"}`)
	if result.code != 0 {
		t.Fatalf("run exit = %d\nstdout: %s\nstderr: %s", result.code, result.stdout, result.stderr)
	}
	got := decodeSingleJSON(t, result.stdout)
	if got["echoed"] != "hello" || got["branch"] != "branch" {
		t.Errorf("result = %#v, want both branches contributing", got)
	}
}

// Declaring the same capabilities in a different order must not change the
// Assembly result mapping.
func TestAssemblyResultDoesNotDependOnDeclarationOrder(t *testing.T) {
	first := writeProject(t)
	buildProject(t, first.root)

	second := writeProject(t)
	reordered := strings.Replace(assemblyYAML(validBinding),
		`  - ref: greet
    entry: ../../capabilities/greet.yaml
  - ref: punctuate
    entry: ../../capabilities/punctuate.yaml`,
		`  - ref: punctuate
    entry: ../../capabilities/punctuate.yaml
  - ref: greet
    entry: ../../capabilities/greet.yaml`, 1)
	writeFile(t, filepath.Join(second.root, "assemblies", second.assembly, "assembly.yaml"), reordered)
	buildProject(t, second.root)

	if got, want := resultSection(t, second.root, second.assembly), resultSection(t, first.root, first.assembly); string(got) != string(want) {
		t.Errorf("result mapping depends on declaration order:\nreordered: %s\noriginal:  %s", got, want)
	}
}

func resultSection(t *testing.T, root, assembly string) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".neuron", "assemblies", assembly, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode canonical manifest: %v", err)
	}
	return manifest.Result
}

// A rebuild after a source change must leave the previous immutable build
// directory untouched and make the new build current.
func TestTwoImmutableBuildsCoexist(t *testing.T) {
	p := writeProject(t)
	first := runNeuron(t, p.root, nil, "build")
	if first.code != 0 {
		t.Fatalf("first build exit = %d\nstdout: %s\nstderr: %s", first.code, first.stdout, first.stderr)
	}

	firstFingerprint := currentFingerprint(t, p.root)
	firstRecord := readFile(t, buildRecordPath(p.root, firstFingerprint))
	firstManifest := readFile(t, filepath.Join(p.root, ".neuron", "assemblies", p.assembly, "manifest.json"))

	writeFile(t, filepath.Join(p.root, "capabilities", "greet.yaml"), strings.Replace(greetCapability, "greeting: hello", "greeting: hey", 1))
	second := runNeuron(t, p.root, nil, "build")
	if second.code != 0 {
		t.Fatalf("second build exit = %d\nstdout: %s\nstderr: %s", second.code, second.stdout, second.stderr)
	}

	secondFingerprint := currentFingerprint(t, p.root)
	if secondFingerprint == firstFingerprint {
		t.Fatal("rebuild reused the first fingerprint despite a source change")
	}
	builds := buildDirs(t, p.root)
	if len(builds) != 2 {
		t.Fatalf("build directories = %v, want both builds retained", builds)
	}
	if got := readFile(t, buildRecordPath(p.root, firstFingerprint)); string(got) != string(firstRecord) {
		t.Error("the previous build record was rewritten")
	}
	if got := readFile(t, filepath.Join(p.root, ".neuron", "assemblies", p.assembly, "manifest.json")); string(got) == string(firstManifest) {
		t.Error("the canonical manifest did not change after a source change")
	}

	startRuntime(t, p.root)
	result := runNeuron(t, p.root, nil, "run", "--params", `{"name":"World"}`)
	if result.code != 0 {
		t.Fatalf("run exit = %d\nstdout: %s\nstderr: %s", result.code, result.stdout, result.stderr)
	}
	if got := decodeSingleJSON(t, result.stdout)["greeting"]; got != "hey" {
		t.Errorf("greeting = %v, want the rebuilt capability configuration", got)
	}
}

func generatedCapabilityYAML(runtimeType string) string {
	return `apiVersion: neuron/v1
kind: Capability

metadata:
  name: generated-echo
  version: 1.0.0

spec:
  capability runtime:
    type: ` + runtimeType + `

  mappings:
    - direction: input
      source: name
      target: name
    - direction: output
      source: message
      target: message
`
}

func generatedAssemblyYAML() string {
	return `apiVersion: neuron/v1
kind: Assembly

capabilities:
  - ref: generated-echo
    entry: ../../capabilities/generated-echo.yaml

result:
  schema:
    - name: message
      type: string
  fields:
    - name: message
      source:
        kind: capabilityResult
        capability: generated-echo
        path: [message]
`
}

// The generated capability runtime template must build through its own
// build.command, install into the store, and execute through the runtime.
func TestGeneratedCapabilityRuntimeBuildsInstallsAndExecutes(t *testing.T) {
	p := generatedRuntimeProject(t, "acctest:echo")
	startRuntime(t, p.root)

	result := runNeuron(t, p.root, nil, "run", "--params", `{"name":"World"}`)
	if result.code != 0 {
		t.Fatalf("run exit = %d\nstdout: %s\nstderr: %s", result.code, result.stdout, result.stderr)
	}
	if got := decodeSingleJSON(t, result.stdout)["message"]; got != "Hello, World!" {
		t.Errorf("message = %v, want the generated capability runtime output", got)
	}
}

// generatedRuntimeProject generates the template capability runtime, wires it
// into an Assembly, and builds the project.
func generatedRuntimeProject(t *testing.T, runtimeType string) project {
	t.Helper()

	p := writeProject(t)
	if res := runNeuron(t, p.root, nil, "provider", "build"); res.code != 0 {
		t.Fatalf("provider build exit = %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
	}
	if res := runNeuron(t, p.root, nil, "new", "capability-runtime", runtimeType); res.code != 0 {
		t.Fatalf("new capability-runtime exit = %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
	}
	writeFile(t, filepath.Join(p.root, "capabilities", "generated-echo.yaml"), generatedCapabilityYAML(runtimeType))
	writeFile(t, filepath.Join(p.root, "assemblies", p.assembly, "assembly.yaml"), generatedAssemblyYAML())
	writeFile(t, filepath.Join(p.root, "assemblies", p.failing, "assembly.yaml"), generatedAssemblyYAML())
	buildProject(t, p.root)
	return p
}

// A frozen build executes from the installed artifact set: removing the local
// source tree after the build must not trigger resolution or installation.
func TestFrozenBuildRunsWithoutLocalSources(t *testing.T) {
	p := generatedRuntimeProject(t, "acctest:frozen")
	if err := os.RemoveAll(filepath.Join(p.root, "neuron")); err != nil {
		t.Fatal(err)
	}

	startRuntime(t, p.root)
	result := runNeuron(t, p.root, nil, "run", "--params", `{"name":"World"}`)
	if result.code != 0 {
		t.Fatalf("run exit = %d\nstdout: %s\nstderr: %s", result.code, result.stdout, result.stderr)
	}
	if got := decodeSingleJSON(t, result.stdout)["message"]; got != "Hello, World!" {
		t.Errorf("message = %v, want the frozen artifact output", got)
	}
}

func currentFingerprint(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".neuron", "current.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pointer struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(data, &pointer); err != nil {
		t.Fatalf("decode current pointer: %v", err)
	}
	if pointer.Fingerprint == "" {
		t.Fatal("current pointer has no fingerprint")
	}
	return pointer.Fingerprint
}

func buildRecordPath(root, fingerprint string) string {
	return filepath.Join(root, ".neuron", "builds", fingerprint, "build.json")
}

func buildDirs(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".neuron", "builds"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
