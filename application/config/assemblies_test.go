package config

import (
	"strings"
	"testing"
)

func TestAssembliesParsedAndExpanded(t *testing.T) {
	cfg, err := loadProjectConfig(t, "neuron.config.json", `{
  "name": "order-platform",
  "version": "0.1.0",
  "lang": "typescript",
  "assemblies": [
    {"name": "orders", "entry": "src/orders.ts", "default": true},
    {"name": "fraud", "entry": "src/fraud.ts"}
  ]
}`)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	assemblies := cfg.EffectiveAssemblies()
	if len(assemblies) != 2 {
		t.Fatalf("EffectiveAssemblies = %d, want 2", len(assemblies))
	}
	if assemblies[0].Name != "orders" || !assemblies[0].Default {
		t.Errorf("assemblies[0] = %#v, want orders default", assemblies[0])
	}
	// Entry paths are expanded against the project root.
	if !strings.HasSuffix(assemblies[0].Entry, "src/orders.ts") {
		t.Errorf("assemblies[0].Entry = %q, want it expanded to .../src/orders.ts", assemblies[0].Entry)
	}

	def, ok := cfg.DefaultAssembly()
	if !ok || def.Name != "orders" {
		t.Errorf("DefaultAssembly = %#v, %v, want orders/true", def, ok)
	}
}

func TestLegacyEntryMigratedToSingleAssembly(t *testing.T) {
	cfg, err := loadProjectConfig(t, "neuron.config.json", `{
  "name": "solo",
  "version": "1.2.3",
  "lang": "yaml",
  "entry": "assembly.yaml"
}`)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	assemblies := cfg.EffectiveAssemblies()
	if len(assemblies) != 1 {
		t.Fatalf("EffectiveAssemblies = %d, want 1 (legacy entry must migrate)", len(assemblies))
	}
	if assemblies[0].Name != "solo" || !assemblies[0].Default {
		t.Errorf("migrated declaration = %#v, want name solo default true", assemblies[0])
	}
	if !strings.HasSuffix(assemblies[0].Entry, "assembly.yaml") {
		t.Errorf("migrated entry = %q, want .../assembly.yaml", assemblies[0].Entry)
	}

	def, ok := cfg.DefaultAssembly()
	if !ok || def.Name != "solo" {
		t.Errorf("DefaultAssembly = %#v, %v, want solo/true", def, ok)
	}
}

func TestEntryAndAssembliesMutuallyExclusive(t *testing.T) {
	_, err := loadProjectConfig(t, "neuron.config.json", `{
  "name": "x",
  "version": "1.0.0",
  "entry": "assembly.ts",
  "assemblies": [{"name": "a", "entry": "a.ts"}]
}`)
	if err == nil {
		t.Fatal("Load succeeded, want `entry` + `assemblies` rejected")
	}
	if !strings.Contains(err.Error(), "assemblies") {
		t.Errorf("error = %v, want it to explain the conflict", err)
	}
}

func TestAssemblyDeclarationsRejected(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "duplicate names",
			content: `{"name":"x","version":"1.0.0","assemblies":[{"name":"a","entry":"a.ts"},{"name":"a","entry":"b.ts"}]}`,
			want:    "duplicate Assembly name",
		},
		{
			name:    "missing entry",
			content: `{"name":"x","version":"1.0.0","assemblies":[{"name":"a"}]}`,
			want:    "`entry` is required",
		},
		{
			name:    "missing name",
			content: `{"name":"x","version":"1.0.0","assemblies":[{"entry":"a.ts"}]}`,
			want:    "`name` is required",
		},
		{
			name:    "invalid name",
			content: `{"name":"x","version":"1.0.0","assemblies":[{"name":"a/b","entry":"a.ts"}]}`,
			want:    "invalid Assembly name",
		},
		{
			name:    "two defaults",
			content: `{"name":"x","version":"1.0.0","assemblies":[{"name":"a","entry":"a.ts","default":true},{"name":"b","entry":"b.ts","default":true}]}`,
			want:    "at most one Assembly may be the default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadProjectConfig(t, "neuron.config.json", tt.content)
			if err == nil {
				t.Fatalf("Load succeeded, want error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestMultiAssemblyNoDefaultRequiresSelection(t *testing.T) {
	cfg, err := loadProjectConfig(t, "neuron.config.json", `{
  "name": "x",
  "version": "1.0.0",
  "assemblies": [{"name": "a", "entry": "a.ts"}, {"name": "b", "entry": "b.ts"}]
}`)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if def, ok := cfg.DefaultAssembly(); ok {
		t.Errorf("DefaultAssembly = %#v, want no default when several Assemblies exist and none is marked", def)
	}
}

func TestGlobalFileDeniesAssemblies(t *testing.T) {
	for _, key := range []string{"assemblies", "entry"} {
		t.Run(key, func(t *testing.T) {
			var content string
			if key == "assemblies" {
				content = "assemblies:\n  - name: a\n    entry: a.ts\n"
			} else {
				content = "entry: assembly.ts\n"
			}
			_, err := loadGlobalConfig(t, content)
			if err == nil {
				t.Fatalf("Load succeeded, want `%s` denied in global config", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error = %v, want it to mention %q", err, key)
			}
		})
	}
}
