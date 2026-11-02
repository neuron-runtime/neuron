package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// TestProjectRuntimeReceivesTheBuiltinPolicy pins the wiring between the
// project's built-in policy and the runtime that enforces it. The policy
// travels with the runtime, because a configuration the runtime never
// receives is a promise the runtime never keeps.
func TestProjectRuntimeReceivesTheBuiltinPolicy(t *testing.T) {
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
