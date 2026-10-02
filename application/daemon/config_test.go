package daemon

import (
	"flag"
	"testing"

	"github.com/neuron-runtime/neuron/application/config"
)

// noreArgs parses the argument list the exact way the nore daemon binary does.
// It mirrors the flag definitions in nore/cmd/nore/main.go so that any argument
// the CLI passes to the daemon is proven parseable, and it asserts that args is
// a pure flag invocation with no stray positional subcommand ("serve").
func noreArgs(t *testing.T, args []string) []string {
	t.Helper()

	fs := flag.NewFlagSet("nore", flag.ContinueOnError)
	_ = fs.String("port", "", "")
	_ = fs.String("socket", "", "")
	_ = fs.Int("workers", 8, "")
	_ = fs.String("data-dir", "", "")
	_ = fs.String("detached-drain-timeout", "30s", "")

	if err := fs.Parse(args); err != nil {
		t.Fatalf("nore flag set rejected daemon args %q: %v", args, err)
	}

	// The real binary stops flag parsing at the first non-flag argument and
	// silently drops everything after it. Any remaining argument therefore
	// means the daemon would start with incomplete configuration.
	return fs.Args()
}

func TestConfigFromEffective(t *testing.T) {
	cfg := config.Config{}
	cfg.Daemon.Socket = "/run/user/1000/neuron/nore.sock"
	cfg.Storage.Directory = "/tmp/neuron-data"
	cfg.Runtime.Workers.Max = 4
	cfg.Daemon.PIDFile = "/tmp/neuron.pid"

	c := ConfigFromEffective(cfg)

	if pos := noreArgs(t, c.Args); len(pos) > 0 {
		t.Fatalf("daemon args contain positional arguments (%q); multi-subcommand invocations are silently ignored by the nore flag parser: %v", pos, c.Args)
	}
	if len(c.Args) == 0 {
		t.Fatal("expected daemon args to carry the effective configuration")
	}
	if c.BinaryPath != "nore-daemon" {
		t.Fatalf("expected default binary path, got %q", c.BinaryPath)
	}
	if c.PIDFile != "/tmp/neuron.pid" {
		t.Fatalf("expected configured PID file, got %q", c.PIDFile)
	}
}

func TestConfigFromEffectiveDefaults(t *testing.T) {
	c := ConfigFromEffective(config.Config{})

	if c.BinaryPath == "" {
		t.Fatal("expected a default nore binary path")
	}
	if pos := noreArgs(t, c.Args); len(pos) > 0 {
		t.Fatalf("default daemon args contain positional arguments: %v", c.Args)
	}
}

func TestConfigFromEffectiveCarriesDetachedDrainTimeout(t *testing.T) {
	cfg := config.Config{}
	cfg.Runtime.DetachedDrainTimeout = "45s"

	c := ConfigFromEffective(cfg)

	if pos := noreArgs(t, c.Args); len(pos) > 0 {
		t.Fatalf("daemon args contain positional arguments: %v", pos)
	}
	if !hasArgPair(c.Args, "--detached-drain-timeout", "45s") {
		t.Fatalf("daemon args %v must carry the configured detached drain timeout", c.Args)
	}
}

func TestConfigFromEffectiveOmitsEmptyDetachedDrainTimeout(t *testing.T) {
	// An empty value means the author declared nothing; the daemon's own
	// default must apply, not an empty flag argument.
	if hasArgPair(ConfigFromEffective(config.Config{}).Args, "--detached-drain-timeout", "") {
		t.Fatal("an empty detached drain timeout must not be forwarded")
	}
}

func hasArgPair(args []string, flagName, value string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == flagName && args[index+1] == value {
			return true
		}
	}
	return false
}
