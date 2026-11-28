package cli

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/application/internal/cli/build"
	"github.com/spf13/cobra"
)

func stubProjectBuild(t *testing.T) {
	t.Helper()
	previous := buildProjectBeforeRun
	buildProjectBeforeRun = func(*cobra.Command, build.Options) error { return nil }
	t.Cleanup(func() { buildProjectBeforeRun = previous })
}

// TestBareNeuronRefusesOutsideProject pins the bare `neuron` command to
// project directories only. The command starts the project runtime, so
// running it outside a project must fail before any runtime is launched —
// ProjectDir alone cannot decide this, because config.Load falls back to the
// working directory for path resolution even when no neuron.config.* exists.
func TestBareNeuronRefusesOutsideProject(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// A fresh directory with no neuron.config.* anywhere between it and the
	// filesystem root (findProjectConfig stops at $HOME, and t.TempDir is
	// outside it).
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	RootCmd.SetArgs([]string{})
	RootCmd.SetOut(io.Discard)
	RootCmd.SetErr(io.Discard)
	err = RootCmd.Execute()
	if err == nil {
		t.Fatal("bare `neuron` outside a project succeeded; want the no-project error")
	}
	if !strings.Contains(err.Error(), "no Neuron project found") {
		t.Errorf("bare `neuron` outside a project error = %q, want the no-project error", err)
	}
}

// TestCommandTreeResolvesDocumentedSurfaces pins the cobra command tree to the
// CLI surface the documentation promises. A Use string containing a space
// ("capability runtime") silently collapses into a single command named after
// its first word, leaving the documented child forms unreachable — this test
// fails if any documented invocation stops resolving to its leaf command.
func TestCommandTreeResolvesDocumentedSurfaces(t *testing.T) {
	cases := []struct {
		name string
		args []string
		leaf string
		rest int
	}{
		{"capability runtime list", []string{"capability", "runtime", "list"}, "list", 0},
		{"capability runtime inspect", []string{"capability", "runtime", "inspect", "example:echo"}, "inspect [name@version]", 1},
		{"capability runtime group", []string{"capability", "runtime"}, "runtime", 0},
		{"instance list", []string{"instance", "list"}, "list [instance-id]", 0},
		{"instance remove", []string{"instance", "remove", "key"}, "remove [instance-id|assembly-key]", 1},
		{"instance clear", []string{"instance", "clear"}, "clear", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, rest, err := RootCmd.Find(tc.args)
			if err != nil {
				t.Fatalf("Find(%v): %v", tc.args, err)
			}
			if cmd.Use != tc.leaf {
				t.Errorf("resolved command Use = %q, want %q", cmd.Use, tc.leaf)
			}
			if len(rest) != tc.rest {
				t.Errorf("unresolved args = %v, want %d remaining", rest, tc.rest)
			}
		})
	}
}

// TestUndocumentedCapabilityListIsNotACommand keeps the accidental
// `neuron capability list` form (a side effect of the old collapsed Use
// string) from reappearing: only `capability runtime list` is the surface.
func TestUndocumentedCapabilityListIsNotACommand(t *testing.T) {
	cmd, rest, err := RootCmd.Find([]string{"capability", "list"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(rest) == 0 {
		t.Errorf("capability list resolved to %q with no remaining args; the list command must live under capability runtime", cmd.Use)
	}
}

// TestBareNeuronBuildsBeforeRuntime proves bare `neuron` invokes the shared
// build pipeline before launching the foreground runtime and forwards --force.
func TestBareNeuronBuildsBeforeRuntime(t *testing.T) {
	dir := t.TempDir()
	eventsFile := filepath.Join(dir, "events.txt")
	fakeNore := filepath.Join(dir, "nore")
	script := "#!/bin/sh\nprintf 'runtime\\n' >> " + eventsFile + "\n"
	if err := os.WriteFile(fakeNore, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake runtime: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "neuron.config.yaml"), []byte(""), 0o644); err != nil {
		t.Fatalf("write project configuration: %v", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	previous := buildProjectBeforeRun
	buildProjectBeforeRun = func(_ *cobra.Command, opts build.Options) error {
		if opts.Root != dir {
			t.Errorf("build root = %q, want %q", opts.Root, dir)
		}
		if !opts.Force {
			t.Error("bare neuron did not forward --force to the build pipeline")
		}
		return os.WriteFile(eventsFile, []byte("build\n"), 0o644)
	}
	t.Cleanup(func() { buildProjectBeforeRun = previous })

	RootCmd.SetArgs([]string{"--nore-path", fakeNore, "--force"})
	RootCmd.SetOut(io.Discard)
	RootCmd.SetErr(io.Discard)
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("bare neuron: %v", err)
	}

	data, err := os.ReadFile(eventsFile)
	if err != nil {
		t.Fatalf("read event order: %v", err)
	}
	if got, want := string(data), "build\nruntime\n"; got != want {
		t.Fatalf("event order = %q, want %q", got, want)
	}
}

// TestBareNeuronStopsWhenTheBuildFails ensures an invalid Assembly cannot
// leave the foreground runtime running with a stale previously built artifact.
func TestBareNeuronStopsWhenTheBuildFails(t *testing.T) {
	dir := t.TempDir()
	runtimeStarted := filepath.Join(dir, "runtime-started")
	fakeNore := filepath.Join(dir, "nore")
	script := "#!/bin/sh\nprintf 'started' > " + runtimeStarted + "\n"
	if err := os.WriteFile(fakeNore, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake runtime: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "neuron.config.yaml"), []byte(""), 0o644); err != nil {
		t.Fatalf("write project configuration: %v", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	previous := buildProjectBeforeRun
	buildProjectBeforeRun = func(*cobra.Command, build.Options) error {
		return errors.New("assembly result mapping is invalid")
	}
	t.Cleanup(func() { buildProjectBeforeRun = previous })

	RootCmd.SetArgs([]string{"--nore-path", fakeNore})
	RootCmd.SetOut(io.Discard)
	RootCmd.SetErr(io.Discard)
	err = RootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "assembly result mapping is invalid") {
		t.Fatalf("bare neuron error = %v, want build error", err)
	}
	if _, statErr := os.Stat(runtimeStarted); !os.IsNotExist(statErr) {
		t.Fatalf("runtime was started despite build failure (stat error: %v)", statErr)
	}
}

// TestProjectRuntimeReceivesDurability pins the deployment-level durability
// flag to the runtime it starts. Durability is rejected in the project file, so
// the flag and environment are the only authoring surfaces that must reach nore.
func TestProjectRuntimeReceivesDurability(t *testing.T) {
	stubProjectBuild(t)
	dir := t.TempDir()

	argsFile := filepath.Join(dir, "nore-args.txt")
	fakeNore := filepath.Join(dir, "nore")
	script := "#!/bin/sh\nfor arg in \"$@\"; do printf '%s\\n' \"$arg\" >> " + argsFile + "; done\n"
	if err := os.WriteFile(fakeNore, []byte(script), 0o755); err != nil {
		t.Fatalf("write the fake runtime: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "neuron.config.yaml"), []byte(""), 0o644); err != nil {
		t.Fatalf("write the project configuration: %v", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	RootCmd.SetArgs([]string{"--nore-path", fakeNore, "--durability", "durable"})
	RootCmd.SetOut(io.Discard)
	RootCmd.SetErr(io.Discard)
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("bare `neuron`: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read the runtime arguments: %v", err)
	}
	args := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--durability" && args[i+1] == "durable" {
			return
		}
	}
	t.Fatalf("the runtime was not started with the durability mode: %v", args)
}

// TestProjectRuntimeReceivesTheBuiltinPolicy pins the wiring between the
// project's built-in policy and the runtime that enforces it. The policy
// travels with the runtime, because a configuration the runtime never
// receives is a promise the runtime never keeps.
func TestProjectRuntimeReceivesTheBuiltinPolicy(t *testing.T) {
	stubProjectBuild(t)
	dir := t.TempDir()

	argsFile := filepath.Join(dir, "nore-args.txt")
	fakeNore := filepath.Join(dir, "nore")
	script := "#!/bin/sh\nfor arg in \"$@\"; do printf '%s\\n' \"$arg\" >> " + argsFile + "; done\n"
	if err := os.WriteFile(fakeNore, []byte(script), 0o755); err != nil {
		t.Fatalf("write the fake runtime: %v", err)
	}

	config := "builtins:\n  http:\n    allowedHosts:\n      - api.example.com\n"
	if err := os.WriteFile(filepath.Join(dir, "neuron.config.yaml"), []byte(config), 0o644); err != nil {
		t.Fatalf("write the project configuration: %v", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	RootCmd.SetArgs([]string{"--nore-path", fakeNore})
	RootCmd.SetOut(io.Discard)
	RootCmd.SetErr(io.Discard)
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("bare `neuron`: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read the runtime arguments: %v", err)
	}
	args := strings.Split(strings.TrimRight(string(data), "\n"), "\n")

	index := -1
	for i, arg := range args {
		if arg == "--builtin-policy" {
			index = i
			break
		}
	}
	if index < 0 || index+1 >= len(args) {
		t.Fatalf("the runtime was not started with a built-in policy: %v", args)
	}

	var policy struct {
		AllowedHTTPHosts []string `json:"allowedHTTPHosts"`
	}
	if err := json.Unmarshal([]byte(args[index+1]), &policy); err != nil {
		t.Fatalf("the built-in policy is not JSON: %v\n%s", err, args[index+1])
	}
	if len(policy.AllowedHTTPHosts) != 1 || policy.AllowedHTTPHosts[0] != "api.example.com" {
		t.Errorf("allowedHTTPHosts = %v, want [api.example.com]", policy.AllowedHTTPHosts)
	}
}

// TestProjectRuntimeReceivesOperationalPolicy proves the second half of the
// project-file denial contract: operational settings are refused there because
// they belong to the deployment, but the global config is the deployment
// surface that must actually reach the runtime. Without explicit forwarding,
// the denial message would name a global config that the runtime never sees.
func TestProjectRuntimeReceivesOperationalPolicy(t *testing.T) {
	stubProjectBuild(t)
	dir := t.TempDir()

	// Point the global config at a temp file. os.UserConfigDir honours
	// XDG_CONFIG_HOME, so the CLI resolves this path without a new flag.
	t.Setenv("XDG_CONFIG_HOME", dir)
	globalDir := filepath.Join(dir, "neuron")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("create the global config directory: %v", err)
	}
	globalConfig := `runtime:
  execution:
    waitDeadlinePolicy: cancel
    maxLifetime: 24h
    timerPoll:
      interval: 3s
      batchSize: 64
`
	if err := os.WriteFile(filepath.Join(globalDir, "config.yaml"), []byte(globalConfig), 0o644); err != nil {
		t.Fatalf("write the global configuration: %v", err)
	}

	argsFile := filepath.Join(dir, "nore-args.txt")
	fakeNore := filepath.Join(dir, "nore")
	script := "#!/bin/sh\nfor arg in \"$@\"; do printf '%s\\n' \"$arg\" >> " + argsFile + "; done\n"
	if err := os.WriteFile(fakeNore, []byte(script), 0o755); err != nil {
		t.Fatalf("write the fake runtime: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "neuron.config.yaml"), []byte(""), 0o644); err != nil {
		t.Fatalf("write the project configuration: %v", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	RootCmd.SetArgs([]string{"--nore-path", fakeNore})
	RootCmd.SetOut(io.Discard)
	RootCmd.SetErr(io.Discard)
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("bare `neuron`: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read the runtime arguments: %v", err)
	}
	args := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for _, want := range [][2]string{
		{"--wait-deadline-policy", "cancel"},
		{"--execution-max-lifetime", "24h"},
		{"--timer-poll-interval", "3s"},
		{"--timer-poll-batch-size", "64"},
	} {
		found := false
		for i := 0; i+1 < len(args); i++ {
			if args[i] == want[0] && args[i+1] == want[1] {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the runtime was not started with %s %s: %v", want[0], want[1], args)
		}
	}
}
