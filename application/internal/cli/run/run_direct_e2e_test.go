package run

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// These end-to-end tests exec a freshly built `neuron` binary against a freshly
// built `nore` binary and a mock capability runtime store, with HOME pointed at
// a temporary directory so nothing touches the developer's real ~/.neuron. The
// working directory is an empty temporary directory, so the tests prove that
// direct invocation needs no Neuron project, build, or running project runtime.

const (
	directEchoSourceDir   = "../../../../examples/capability-runtimes/echo"
	directNoreSourceDir   = "../../../../nore/cmd/nore"
	directNeuronSourceDir = "../../../cmd/neuron"
	directTestDigest      = "sha256:test"
)

var directE2E struct {
	neuronBin string
	noreBin   string
	home      string
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "neuron-run-direct-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	native := filepath.Join(dir, "echo")
	wasm := filepath.Join(dir, "echo.wasm")

	build := func(binDir, out string, env []string) error {
		cmd := exec.Command("go", "build", "-o", out, ".")
		cmd.Dir = binDir
		cmd.Env = append(os.Environ(), append([]string{"GOWORK=off"}, env...)...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("go build %s: %v\n%s", out, err, stderr.String())
		}
		return nil
	}

	if err := build(directEchoSourceDir, native, nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := build(directEchoSourceDir, wasm, []string{"GOOS=wasip1", "GOARCH=wasm"}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// The nore and neuron binaries are built inside their modules, so the
	// workspace stays enabled for them.
	buildWorkspace := func(binDir, out string) error {
		cmd := exec.Command("go", "build", "-o", out, ".")
		cmd.Dir = binDir
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("go build %s: %v\n%s", out, err, stderr.String())
		}
		return nil
	}

	noreBin := filepath.Join(dir, "nore")
	if err := buildWorkspace(directNoreSourceDir, noreBin); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	neuronBin := filepath.Join(dir, "neuron")
	if err := buildWorkspace(directNeuronSourceDir, neuronBin); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// A fake HOME whose default capability runtime store holds the echo
	// fixtures laid out the way `neuron add` installs them.
	home := filepath.Join(dir, "home")
	store := filepath.Join(home, ".neuron", "capabilityRuntimes")

	install := func(typ, entry, runtimeKind, src string) error {
		path, err := capabilityrt.TypePath(typ)
		if err != nil {
			return err
		}
		artifactDir := filepath.Join(store, path, "1.0.0", directTestDigest)
		if err := os.MkdirAll(artifactDir, 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(artifactDir, entry), data, 0o755); err != nil {
			return err
		}

		// The store resolves an installed artifact by reading its install.json
		// record, so a fixture is only "installed" once that record exists.
		record := map[string]any{
			"type":     typ,
			"version":  "1.0.0",
			"digest":   directTestDigest,
			"registry": "local",
			"rootDir":  artifactDir,
			"runtime": map[string]any{
				"type":       runtimeKind,
				"entrypoint": entry,
				"protocol":   "neuron/capability-runtime-v1-json",
			},
		}
		recData, err := json.Marshal(record)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(artifactDir, "install.json"), recData, 0o644)
	}

	if err := install("example:echo", "echo", "process", native); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := install("example:echo-wasm", "echo.wasm", "wasm", wasm); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	directE2E.neuronBin = neuronBin
	directE2E.noreBin = noreBin
	directE2E.home = home

	os.Exit(m.Run())
}

// runNeuron execs the neuron binary in an empty temporary directory outside any
// project, with HOME pointed at the fake home.
func runNeuron(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	cwd := t.TempDir()
	all := append([]string{"--nore-path", directE2E.noreBin}, args...)
	cmd := exec.Command(directE2E.neuronBin, all...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+directE2E.home)

	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	_ = cmd.Run()
	return out.String(), errBuf.String(), cmd.ProcessState.ExitCode()
}

func TestRunDirectProcessRuntime(t *testing.T) {
	stdout, stderr, code := runNeuron(t, "run", "-c", "example:echo", "--params", `{"value":"hello"}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout: %s", err, stdout)
	}
	if result["type"] != "example:echo" {
		t.Errorf("result.type = %v, want example:echo", result["type"])
	}
	if result["value"] != "hello" {
		t.Errorf("result.value = %v, want hello", result["value"])
	}
}

func TestRunDirectWasmRuntime(t *testing.T) {
	stdout, stderr, code := runNeuron(t, "run", "-c", "example:echo-wasm", "--params", `{"value":"hello"}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout: %s", err, stdout)
	}
	if result["type"] != "example:echo-wasm" {
		t.Errorf("result.type = %v, want example:echo-wasm", result["type"])
	}
}

func TestRunDirectParamsFile(t *testing.T) {
	paramsPath := filepath.Join(t.TempDir(), "params.json")
	if err := os.WriteFile(paramsPath, []byte(`{"value":"from-file"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runNeuron(t, "run", "-c", "example:echo", "--params-file", paramsPath)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout: %s", err, stdout)
	}
	if result["value"] != "from-file" {
		t.Errorf("result.value = %v, want from-file", result["value"])
	}
}

func TestRunDirectCoreRuntime(t *testing.T) {
	// Core runtimes are built into N.O.R.E.: they resolve without any store
	// fixture, and legacy bare names canonicalize to the neuron:core namespace.
	cases := []struct {
		name      string
		reference string
	}{
		{name: "canonical", reference: "neuron:core:set"},
		{name: "legacy bare", reference: "set"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runNeuron(t, "run", "-c", tc.reference, "--params", `{"value":"hello"}`)
			if code != 0 {
				t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}

			var result map[string]any
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatalf("stdout is not JSON: %v\nstdout: %s", err, stdout)
			}
			if result["value"] != "hello" {
				t.Errorf("result.value = %v, want hello", result["value"])
			}
		})
	}
}

// snapshotStore captures every file under the default capability runtime store
// so a test can prove an invocation did not write to it.
func snapshotStore(t *testing.T) map[string]string {
	t.Helper()

	root := filepath.Join(directE2E.home, ".neuron", "capabilityRuntimes")
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return out
}

func assertStoreUnchanged(t *testing.T, before map[string]string) {
	t.Helper()
	after := snapshotStore(t)
	if len(before) != len(after) {
		t.Fatalf("capability runtime store changed: %d entries before, %d after", len(before), len(after))
	}
	for path, content := range before {
		got, ok := after[path]
		if !ok {
			t.Errorf("capability runtime store entry %q disappeared", path)
			continue
		}
		if got != content {
			t.Errorf("capability runtime store entry %q was rewritten", path)
		}
	}
}

func TestRunDirectFailures(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{
			name: "not installed",
			args: []string{"run", "-c", "example:missing"},
		},
		{
			name: "invalid version",
			args: []string{"run", "-c", "example:echo@not-a-version"},
		},
		{
			name: "conflicting selector",
			args: []string{"run", "-c", "example:echo", "some-assembly"},
		},
		{
			name: "remote conflict",
			args: []string{"run", "-c", "example:echo", "--remote", "http://127.0.0.1:1"},
		},
		{
			// Core runtimes are never installed, so a version suffix is a
			// contradiction the CLI rejects before touching the store.
			name: "core runtime with version",
			args: []string{"run", "-c", "neuron:core:set@1.0.0"},
		},
		{
			name: "unknown core runtime",
			args: []string{"run", "-c", "neuron:core:does-not-exist"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := snapshotStore(t)
			stdout, stderr, code := runNeuron(t, tc.args...)
			if code == 0 {
				t.Fatalf("exit = 0, want non-zero\nstdout: %s", stdout)
			}
			if stdout != "" {
				t.Errorf("stdout is not empty on failure: %s", stdout)
			}
			if stderr == "" {
				t.Errorf("stderr is empty, want a diagnostic")
			}
			assertStoreUnchanged(t, before)
			if tc.name == "not installed" && !strings.Contains(stderr, "neuron add") {
				t.Errorf("missing-dependency error must point at `neuron add`, got: %s", stderr)
			}
		})
	}
}
