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
	_ = fs.String("storage-driver", "", "")
	_ = fs.String("storage-path", "", "")
	_ = fs.String("storage-dsn", "", "")
	_ = fs.String("detached-drain-timeout", "30s", "")
	_ = fs.String("durability", "", "")
	_ = fs.String("wait-deadline-policy", "fail", "")
	_ = fs.String("execution-max-lifetime", "720h", "")
	_ = fs.String("timer-poll-interval", "1s", "")
	_ = fs.Int("timer-poll-batch-size", 256, "")

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

func TestConfigFromEffectiveCarriesDurability(t *testing.T) {
	cfg := config.Config{}
	cfg.Runtime.Execution.Durability = config.DurabilityDurable

	c := ConfigFromEffective(cfg)

	if pos := noreArgs(t, c.Args); len(pos) > 0 {
		t.Fatalf("daemon args contain positional arguments: %v", pos)
	}
	if !hasArgPair(c.Args, "--durability", config.DurabilityDurable) {
		t.Fatalf("daemon args %v must carry the configured durability mode", c.Args)
	}
}

func TestConfigFromEffectiveOmitsEmptyDurability(t *testing.T) {
	if hasArgPair(ConfigFromEffective(config.Config{}).Args, "--durability", "") {
		t.Fatal("an empty durability mode must not be forwarded")
	}
}

func TestConfigFromEffectiveCarriesOperationalPolicy(t *testing.T) {
	cfg := config.Config{}
	cfg.Runtime.Execution.WaitDeadlinePolicy = "cancel"
	cfg.Runtime.Execution.MaxLifetime = "24h"
	cfg.Runtime.Execution.TimerPoll.Interval = "3s"
	cfg.Runtime.Execution.TimerPoll.BatchSize = 64

	c := ConfigFromEffective(cfg)

	if pos := noreArgs(t, c.Args); len(pos) > 0 {
		t.Fatalf("daemon args contain positional arguments: %v", pos)
	}
	for _, want := range [][2]string{
		{"--wait-deadline-policy", "cancel"},
		{"--execution-max-lifetime", "24h"},
		{"--timer-poll-interval", "3s"},
		{"--timer-poll-batch-size", "64"},
	} {
		if !hasArgPair(c.Args, want[0], want[1]) {
			t.Errorf("daemon args %v must carry %s %s", c.Args, want[0], want[1])
		}
	}
}

func TestConfigFromEffectiveOmitsEmptyOperationalPolicy(t *testing.T) {
	// Zero values mean "not declared"; the runtime's own defaults must apply
	// rather than an empty flag argument or an invalid zero batch size.
	args := ConfigFromEffective(config.Config{}).Args
	for _, name := range []string{
		"--wait-deadline-policy",
		"--execution-max-lifetime",
		"--timer-poll-interval",
		"--timer-poll-batch-size",
	} {
		for _, value := range args {
			if value == name {
				t.Errorf("daemon args %v must not forward an undeclared %s", args, name)
			}
		}
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
