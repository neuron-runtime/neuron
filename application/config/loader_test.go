package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(Options{GlobalPath: "/nonexistent/global.yaml", ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Lang != "typescript" {
		t.Errorf("default lang = %q, want typescript", cfg.Lang)
	}
	if cfg.Runtime.Execution.Mode != "wait" {
		t.Errorf("default mode = %q, want wait", cfg.Runtime.Execution.Mode)
	}
	if cfg.Runtime.Execution.Timeout != "30m" {
		t.Errorf("default timeout = %q, want 30m", cfg.Runtime.Execution.Timeout)
	}
	if len(cfg.Executors.Registries) != 0 {
		t.Errorf("default registries = %d, want 0 (none compiled in)", len(cfg.Executors.Registries))
	}
	if len(cfg.Executors.DefaultRegistries) != 1 || cfg.Executors.DefaultRegistries[0] != "local" {
		t.Errorf("default defaultRegistries = %v, want [local]", cfg.Executors.DefaultRegistries)
	}
}

func TestProjectPartialOverridePreservesGlobal(t *testing.T) {
	dir := t.TempDir()

	global := write(t, dir, "global.yaml", `
runtime:
  workers:
    min: 4
    max: 8
`)
	write(t, dir, "neuron.config.yaml", `
runtime:
  workers:
    max: 16
`)

	cfg, err := Load(Options{
		GlobalPath:  global,
		ProjectPath: filepath.Join(dir, "neuron.config.yaml"),
	})
	if err != nil {
		t.Fatal(err)
	}

	// The project only overrode max; min must survive from global.
	if cfg.Runtime.Workers.Min != 4 {
		t.Errorf("min = %d, want 4 (preserved from global)", cfg.Runtime.Workers.Min)
	}
	if cfg.Runtime.Workers.Max != 16 {
		t.Errorf("max = %d, want 16", cfg.Runtime.Workers.Max)
	}
}

func TestCLIOverridesProject(t *testing.T) {
	dir := t.TempDir()

	write(t, dir, "neuron.config.json", `
{
  "runtime": {
    "execution": {
      "mode": "wait",
      "timeout": "10m"
    }
  }
}
`)

	cfg, err := Load(Options{
		GlobalPath:  "/nonexistent/global.yaml",
		ProjectPath: filepath.Join(dir, "neuron.config.json"),
		CLI: map[string]any{
			"runtime.execution.mode": "detach",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Runtime.Execution.Mode != "detach" {
		t.Errorf("mode = %q, want detach (CLI wins)", cfg.Runtime.Execution.Mode)
	}
	if cfg.Runtime.Execution.Timeout != "10m" {
		t.Errorf("timeout = %q, want 10m (project preserved)", cfg.Runtime.Execution.Timeout)
	}
}

func TestDefaultTimeoutSurvivesProjectModeOverride(t *testing.T) {
	dir := t.TempDir()

	// Project only sets mode; timeout should fall back to the 30m default.
	write(t, dir, "neuron.config.yaml", `
runtime:
  execution:
    mode: detach
`)

	cfg, err := Load(Options{
		GlobalPath:  "/nonexistent/global.yaml",
		ProjectPath: filepath.Join(dir, "neuron.config.yaml"),
	})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Runtime.Execution.Mode != "detach" {
		t.Errorf("mode = %q, want detach", cfg.Runtime.Execution.Mode)
	}
	if cfg.Runtime.Execution.Timeout != "30m" {
		t.Errorf("timeout = %q, want 30m default", cfg.Runtime.Execution.Timeout)
	}
}

func TestEntryExpandsAgainstProjectRoot(t *testing.T) {
	dir := t.TempDir()

	write(t, dir, "neuron.config.yaml", `
lang: typescript
entry: system.ts
`)

	cfg, err := Load(Options{
		GlobalPath:  "/nonexistent/global.yaml",
		ProjectDir:  dir,
		ProjectPath: filepath.Join(dir, "neuron.config.yaml"),
	})
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(dir, "system.ts")
	if cfg.Entry != want {
		t.Errorf("entry = %q, want %q", cfg.Entry, want)
	}
}

func TestDiscoveryPrefersJSON(t *testing.T) {
	dir := t.TempDir()

	write(t, dir, "neuron.yaml", `
runtime:
  workers:
    max: 8
`)
	write(t, dir, "neuron.config.json", `{"runtime": {"workers": {"max": 32}}}`)

	cfg, err := Load(Options{GlobalPath: "/nonexistent/global.yaml", ProjectDir: dir})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Runtime.Workers.Max != 32 {
		t.Errorf("max = %d, want 32 (neuron.config.json wins over legacy neuron.yaml)", cfg.Runtime.Workers.Max)
	}
}

func TestLegacyConfigRejected(t *testing.T) {
	for _, name := range []string{"neuron.yaml", "neuron.yml"} {
		dir := t.TempDir()
		write(t, dir, name, "lang: yaml\n")

		_, err := Load(Options{GlobalPath: "/nonexistent/global.yaml", ProjectDir: dir})
		if err == nil {
			t.Errorf("%s: Load succeeded, want legacy name to be rejected", name)
			continue
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("%s: error = %v, want it to mention the removed name", name, err)
		}
	}
}

func TestExplicitLegacyConfigRejected(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "neuron.yaml", "lang: yaml\n")

	_, err := Load(Options{
		GlobalPath:  "/nonexistent/global.yaml",
		ProjectPath: filepath.Join(dir, "neuron.yaml"),
	})
	if err == nil {
		t.Fatal("Load succeeded, want explicit legacy name rejected")
	}
	if !strings.Contains(err.Error(), "neuron.yaml") {
		t.Errorf("error = %v, want it to mention the removed name", err)
	}
}

func TestVariablesFromConfig(t *testing.T) {
	dir := t.TempDir()

	write(t, dir, "neuron.config.yaml", `
lang: yaml
variables:
  environment: development
  limits:
    timeout: 10s
    retries: 3
`)

	cfg, err := Load(Options{
		GlobalPath:  "/nonexistent/global.yaml",
		ProjectPath: filepath.Join(dir, "neuron.config.yaml"),
	})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Variables["environment"] != "development" {
		t.Errorf("variables.environment = %v, want development", cfg.Variables["environment"])
	}
	limits, ok := cfg.Variables["limits"].(map[string]any)
	if !ok {
		t.Fatalf("variables.limits = %#v, want nested map", cfg.Variables["limits"])
	}
	if limits["timeout"] != "10s" {
		t.Errorf("variables.limits.timeout = %v, want 10s", limits["timeout"])
	}
}

func TestDefaultVariablesEmpty(t *testing.T) {
	cfg, err := Load(Options{GlobalPath: "/nonexistent/global.yaml", ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Variables) != 0 {
		t.Errorf("default variables = %#v, want empty", cfg.Variables)
	}
}

func TestExecutorRegistriesFromProjectConfig(t *testing.T) {
	dir := t.TempDir()

	write(t, dir, "neuron.config.yaml", `
executors:
  registries:
    - name: local
      url: ./executors
    - name: github
      url: https://registry.neuron.dev
`)

	cfg, err := Load(Options{
		GlobalPath:  "/nonexistent/global.yaml",
		ProjectPath: filepath.Join(dir, "neuron.config.yaml"),
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Executors.Registries) != 2 {
		t.Fatalf("registries = %d, want 2", len(cfg.Executors.Registries))
	}
	if cfg.Executors.Registries[0].Name != "local" || cfg.Executors.Registries[0].URL == "" {
		t.Errorf("registries[0] = %#v, want named local", cfg.Executors.Registries[0])
	}
	if len(cfg.Executors.DefaultRegistries) != 1 || cfg.Executors.DefaultRegistries[0] != "local" {
		t.Errorf("defaultRegistries = %v, want [local] preserved", cfg.Executors.DefaultRegistries)
	}
}

func TestRejectStorageKey(t *testing.T) {
	dir := t.TempDir()

	write(t, dir, "neuron.config.yaml", `
storage:
  directory: ./data
`)

	_, err := Load(Options{GlobalPath: "/nonexistent/global.yaml", ProjectPath: filepath.Join(dir, "neuron.config.yaml")})
	if err == nil {
		t.Fatal("Load succeeded, want error for `storage` in project config")
	}
	if !strings.Contains(err.Error(), "storage") {
		t.Errorf("error = %v, want it to mention `storage`", err)
	}
}

func TestRejectStoreDirKey(t *testing.T) {
	dir := t.TempDir()

	write(t, dir, "neuron.config.yaml", `
executors:
  storeDir: /somewhere
`)

	_, err := Load(Options{GlobalPath: "/nonexistent/global.yaml", ProjectPath: filepath.Join(dir, "neuron.config.yaml")})
	if err == nil {
		t.Fatal("Load succeeded, want error for `executors.storeDir` in project config")
	}
	if !strings.Contains(err.Error(), "storeDir") {
		t.Errorf("error = %v, want it to mention `storeDir`", err)
	}
}

func TestGlobalStorageKeyRejected(t *testing.T) {
	dir := t.TempDir()

	global := write(t, dir, "global.yaml", `
storage:
  provider: postgres
`)

	_, err := Load(Options{GlobalPath: global, ProjectDir: t.TempDir()})
	if err == nil {
		t.Fatal("Load succeeded, want error for `storage` in global config")
	}
}

func TestDiscoveryWalksUpward(t *testing.T) {
	root := t.TempDir()
	write(t, root, "neuron.config.json", `{"lang":"typescript"}`)
	subDir := filepath.Join(root, "services", "orders")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Options{GlobalPath: "/nonexistent/global.yaml", ProjectDir: subDir})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Lang != "typescript" {
		t.Errorf("lang = %q, want typescript discovered from ancestor", cfg.Lang)
	}
	if cfg.ProjectDir != root {
		t.Errorf("ProjectDir = %q, want %q (the config's directory)", cfg.ProjectDir, root)
	}
}

func TestProjectDirUsesConfigDirectoryWhenFlagOnSubdir(t *testing.T) {
	root := t.TempDir()
	write(t, root, "neuron.config.json", `{}`)
	subDir := filepath.Join(root, "nested")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Options{GlobalPath: "/nonexistent/global.yaml", ProjectDir: subDir})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectDir != root {
		t.Errorf("ProjectDir = %q, want %q", cfg.ProjectDir, root)
	}
}

func TestMultipleConfigCandidatesWarned(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "neuron.config.json", `{"lang":"typescript"}`)
	write(t, dir, "neuron.config.yaml", "lang: yaml\n")

	cfg, err := Load(Options{GlobalPath: "/nonexistent/global.yaml", ProjectDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Lang != "typescript" {
		t.Errorf("lang = %q, want typescript (json preferred)", cfg.Lang)
	}
	if len(cfg.Warnings) == 0 {
		t.Fatal("expected a warning about multiple config candidates")
	}
}

func TestLocalRootsExpandedAgainstProjectDir(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "neuron.config.json", `{
  "executors": { "localRoots": ["./custom", "./myown"] }
}`)

	cfg, err := Load(Options{GlobalPath: "/nonexistent/global.yaml", ProjectDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Executors.LocalRoots) != 2 {
		t.Fatalf("localRoots = %d, want 2", len(cfg.Executors.LocalRoots))
	}
	if cfg.Executors.LocalRoots[0] != filepath.Join(dir, "custom") {
		t.Errorf("localRoots[0] = %q, want %q", cfg.Executors.LocalRoots[0], filepath.Join(dir, "custom"))
	}
}

func TestLocalRegistryURLExpandedAgainstProjectDir(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "neuron.config.yaml", `
executors:
  registries:
    - name: local
      url: ./executors
`)

	cfg, err := Load(Options{GlobalPath: "/nonexistent/global.yaml", ProjectDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Executors.Registries) != 1 {
		t.Fatalf("registries = %d, want 1", len(cfg.Executors.Registries))
	}
	if cfg.Executors.Registries[0].URL != filepath.Join(dir, "executors") {
		t.Errorf("local registry url = %q, want %q", cfg.Executors.Registries[0].URL, filepath.Join(dir, "executors"))
	}
}

func TestDevMaxWorkersDefaultsToOne(t *testing.T) {
	cfg, err := Load(Options{GlobalPath: "/nonexistent/global.yaml", ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Dev.MaxWorkers != 1 {
		t.Errorf("default dev.maxWorkers = %d, want 1", cfg.Dev.MaxWorkers)
	}
}

func TestDevMaxWorkersFromProjectConfig(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "neuron.config.json", `{"dev":{"maxWorkers":4}}`)

	cfg, err := Load(Options{GlobalPath: "/nonexistent/global.yaml", ProjectDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Dev.MaxWorkers != 4 {
		t.Errorf("dev.maxWorkers = %d, want 4", cfg.Dev.MaxWorkers)
	}
}
