package initcmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/application/project"
	"gopkg.in/yaml.v3"
)

// scaffoldConfigPath is the project configuration every scaffold writes.
const scaffoldConfigPath = "neuron.config.json"

// nonExistentGlobalPath keeps project-scaffold tests hermetic: config.Load
// skips a global file that does not exist, so the developer's own
// ~/.config/neuron/config.yaml never leaks into the assertions.
const nonExistentGlobalPath = "/nonexistent/global.yaml"

// scaffoldedConfig is the subset of neuron.config.json these tests assert on.
type scaffoldedConfig struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Lang    string `json:"lang"`
	Entry   string `json:"entry"`
	Runtime struct {
		Host string          `json:"host"`
		Port json.RawMessage `json:"port"`
	} `json:"runtime"`
}

// parseScaffoldConfig reads and decodes the scaffolded neuron.config.json,
// failing the test when the file is missing or is not valid JSON.
func parseScaffoldConfig(t *testing.T, root string) scaffoldedConfig {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, scaffoldConfigPath))
	if err != nil {
		t.Fatalf("read %s: %v", scaffoldConfigPath, err)
	}
	var cfg scaffoldedConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode %s as JSON: %v\n%s", scaffoldConfigPath, err, data)
	}
	return cfg
}

// assertScaffoldIdentity checks the project identity and runtime endpoint the
// scaffolded configuration must carry: neuron.config.* is the single source
// of the Assembly identity.
func assertScaffoldIdentity(t *testing.T, root, name string) {
	t.Helper()

	cfg := parseScaffoldConfig(t, root)

	if cfg.Name != name {
		t.Errorf("config name = %q, want %q", cfg.Name, name)
	}
	if cfg.Version != scaffoldAssemblyVersion {
		t.Errorf("config version = %q, want %q", cfg.Version, scaffoldAssemblyVersion)
	}
	if cfg.Runtime.Host != "localhost" {
		t.Errorf("config runtime.host = %q, want localhost", cfg.Runtime.Host)
	}
	if port := strings.TrimSpace(string(cfg.Runtime.Port)); port != `"auto"` {
		t.Errorf("config runtime.port = %s, want \"auto\"", port)
	}
}

// assertTypeScriptAuthorsNoIdentity checks that the scaffolded TypeScript
// assembly authors no Assembly identity at all: neuron.config.* owns
// name/version, so a scaffold that repeats them would recreate the duplication
// the shared build seam exists to remove.
func assertTypeScriptAuthorsNoIdentity(t *testing.T, root, assemblyFile string) {
	t.Helper()

	source, err := os.ReadFile(filepath.Join(root, assemblyFile))
	if err != nil {
		t.Fatalf("read %s: %v", assemblyFile, err)
	}
	if !strings.Contains(string(source), "Assembly()") {
		t.Errorf("%s must use the zero-argument Assembly() constructor:\n%s", assemblyFile, source)
	}
	if strings.Contains(string(source), "Assembly({") {
		t.Errorf("%s must not author Assembly identity:\n%s", assemblyFile, source)
	}
	if strings.Contains(string(source), scaffoldAssemblyVersion) {
		t.Errorf("%s must not author version %q; neuron.config.* is the only source:\n%s", assemblyFile, scaffoldAssemblyVersion, source)
	}
}

// assertConfigOwnsIdentity checks that the scaffolded YAML assembly authors
// no Assembly identity at all: neuron.config.* owns name/version, so a
// scaffold that repeats them would recreate the duplication the shared build
// seam exists to remove.
func assertConfigOwnsIdentity(t *testing.T, root, assemblyFile string) {
	t.Helper()

	source, err := os.ReadFile(filepath.Join(root, assemblyFile))
	if err != nil {
		t.Fatalf("read %s: %v", assemblyFile, err)
	}
	var af project.AssemblyFile
	if err := yaml.Unmarshal(source, &af); err != nil {
		t.Fatalf("decode %s as an assembly file: %v\n%s", assemblyFile, err, source)
	}
	if af.Metadata.Name != "" || af.Metadata.Version != "" {
		t.Errorf("%s authors assembly identity %+v; neuron.config.* must be the only source", assemblyFile, af.Metadata)
	}
}

// loadScaffolded runs the strict configuration loader over the scaffolded
// project. Any unknown or engine-plumbing key in the generated file makes
// Load fail, so a passing load proves the scaffold emits only legal keys.
func loadScaffolded(t *testing.T, root string) config.Config {
	t.Helper()

	cfg, err := config.Load(config.Options{
		ProjectDir: root,
		GlobalPath: nonExistentGlobalPath,
	})
	if err != nil {
		t.Fatalf("load scaffolded project configuration: %v", err)
	}
	return cfg
}

func TestScaffoldTypeScriptConfig(t *testing.T) {
	root := t.TempDir()
	if err := scaffoldTypeScript(root, "hello-ts-config"); err != nil {
		t.Fatal(err)
	}

	assertScaffoldIdentity(t, root, "hello-ts-config")
	assertTypeScriptAuthorsNoIdentity(t, root, "assembly.ts")

	cfg := loadScaffolded(t, root)
	if cfg.Name != "hello-ts-config" {
		t.Errorf("cfg.Name = %q, want hello-ts-config", cfg.Name)
	}
	if cfg.Version != scaffoldAssemblyVersion {
		t.Errorf("cfg.Version = %q, want %q", cfg.Version, scaffoldAssemblyVersion)
	}
	if cfg.Runtime.Host != "localhost" {
		t.Errorf("cfg.Runtime.Host = %q, want localhost", cfg.Runtime.Host)
	}
	if !cfg.Runtime.Port.IsAuto() {
		t.Errorf("cfg.Runtime.Port = %+v, want auto", cfg.Runtime.Port)
	}
	if cfg.Lang != "typescript" {
		t.Errorf("cfg.Lang = %q, want typescript", cfg.Lang)
	}
}

func TestScaffoldYAMLConfig(t *testing.T) {
	root := t.TempDir()
	if err := scaffoldYAML(root, "hello-yaml-config"); err != nil {
		t.Fatal(err)
	}

	assertScaffoldIdentity(t, root, "hello-yaml-config")
	assertConfigOwnsIdentity(t, root, "assembly.yaml")

	cfg := loadScaffolded(t, root)
	if cfg.Name != "hello-yaml-config" {
		t.Errorf("cfg.Name = %q, want hello-yaml-config", cfg.Name)
	}
	if cfg.Version != scaffoldAssemblyVersion {
		t.Errorf("cfg.Version = %q, want %q", cfg.Version, scaffoldAssemblyVersion)
	}
	if cfg.Runtime.Host != "localhost" {
		t.Errorf("cfg.Runtime.Host = %q, want localhost", cfg.Runtime.Host)
	}
	if !cfg.Runtime.Port.IsAuto() {
		t.Errorf("cfg.Runtime.Port = %+v, want auto", cfg.Runtime.Port)
	}
	if cfg.Lang != "yaml" {
		t.Errorf("cfg.Lang = %q, want yaml", cfg.Lang)
	}
}
