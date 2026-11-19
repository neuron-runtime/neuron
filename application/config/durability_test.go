package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDurabilityDefaultsToHistory(t *testing.T) {
	cfg, err := Load(Options{GlobalPath: "/nonexistent/global.yaml", ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.Execution.Durability != DurabilityHistory {
		t.Errorf("default durability = %q, want %q", cfg.Runtime.Execution.Durability, DurabilityHistory)
	}
}

func TestDurabilityAcceptsSupportedModes(t *testing.T) {
	for _, mode := range []string{DurabilityNone, DurabilityHistory, DurabilityDurable} {
		t.Run(mode, func(t *testing.T) {
			cfg, err := Load(Options{
				GlobalPath: "/nonexistent/global.yaml",
				ProjectDir: t.TempDir(),
				CLI:        map[string]any{"runtime.execution.durability": mode},
			})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Runtime.Execution.Durability != mode {
				t.Errorf("durability = %q, want %q", cfg.Runtime.Execution.Durability, mode)
			}
		})
	}
}

func TestDurabilityUnknownValueIsRejected(t *testing.T) {
	_, err := Load(Options{
		GlobalPath: "/nonexistent/global.yaml",
		ProjectDir: t.TempDir(),
		CLI:        map[string]any{"runtime.execution.durability": "sometimes"},
	})
	if err == nil {
		t.Fatal("expected an error for an unknown durability mode")
	}
	for _, want := range []string{"sometimes", DurabilityNone, DurabilityHistory, DurabilityDurable} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestDurabilityEnvironmentOverridesGlobal(t *testing.T) {
	dir := t.TempDir()
	global := write(t, dir, "global.yaml", `
runtime:
  execution:
    durability: history
`)
	t.Setenv("NEURON_EXECUTION_DURABILITY", DurabilityDurable)

	cfg, err := Load(Options{
		GlobalPath:  global,
		ProjectDir:  dir,
		Environment: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.Execution.Durability != DurabilityDurable {
		t.Errorf("durability = %q, want %q from the environment", cfg.Runtime.Execution.Durability, DurabilityDurable)
	}
}

func TestProjectFileRejectsDurability(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "neuron.config.yaml", `
runtime:
  execution:
    durability: durable
`)

	_, err := Load(Options{
		GlobalPath:  "/nonexistent/global.yaml",
		ProjectPath: filepath.Join(dir, "neuron.config.yaml"),
	})
	if err == nil {
		t.Fatal("expected the project file to reject durability")
	}
	if !strings.Contains(err.Error(), "runtime.execution.durability") {
		t.Errorf("error %q does not name runtime.execution.durability", err)
	}
	if !strings.Contains(err.Error(), "deployment-level") {
		t.Errorf("error %q does not explain that durability is deployment-level", err)
	}
}

func TestRetentionPolicyLoadsFromConfiguration(t *testing.T) {
	cfg, err := Load(Options{
		GlobalPath: "/nonexistent/global.yaml",
		ProjectDir: t.TempDir(),
		CLI: map[string]any{
			"runtime.execution.retention.maxAge":                 "168h",
			"runtime.execution.retention.maxArtifactsPerVersion": 3,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.Execution.Retention.MaxAge != "168h" {
		t.Errorf("retention maxAge = %q, want 168h", cfg.Runtime.Execution.Retention.MaxAge)
	}
	if cfg.Runtime.Execution.Retention.MaxArtifactsPerVersion != 3 {
		t.Errorf("retention maxArtifactsPerVersion = %d, want 3", cfg.Runtime.Execution.Retention.MaxArtifactsPerVersion)
	}
}
