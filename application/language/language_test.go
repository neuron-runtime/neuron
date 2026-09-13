package language_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Muhammad-Jay/neuron/application/language"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		in   string
		want language.Language
	}{
		{"yaml", language.YAML},
		{"yml", language.YAML},
		{"typescript", language.TypeScript},
		{"ts", language.TypeScript},
		{" YAML ", language.YAML},
		{"TypeScript", language.TypeScript},
	}
	for _, tc := range cases {
		got, err := language.Normalize(tc.in)
		if err != nil {
			t.Fatalf("Normalize(%q) error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeUnknown(t *testing.T) {
	if _, err := language.Normalize("python"); !errors.Is(err, language.ErrLanguageUnknown) {
		t.Errorf("Normalize(python) error = %v, want ErrLanguageUnknown", err)
	}
}

func TestResolveFlagWinsOverConfig(t *testing.T) {
	got, err := language.Resolve("ts", "yaml", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != language.TypeScript {
		t.Errorf("Resolve = %q, want typescript", got)
	}
}

func TestResolveFallsBackToConfig(t *testing.T) {
	got, err := language.Resolve("", "yml", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != language.YAML {
		t.Errorf("Resolve = %q, want yaml", got)
	}
}

func TestResolveDetectsTypeScriptEntry(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.ts"), []byte("export {}"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := language.Resolve("", "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != language.TypeScript {
		t.Errorf("Resolve = %q, want typescript", got)
	}
}

func TestResolveFlagOverridesDetection(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.ts"), []byte("export {}"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := language.Resolve("yaml", "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != language.YAML {
		t.Errorf("Resolve = %q, want yaml", got)
	}
}

func TestResolveDetectsYAMLSystemFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "system.yaml"), []byte("apiVersion: neuron/v1\nkind: System\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := language.Resolve("", "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != language.YAML {
		t.Errorf("Resolve = %q, want yaml (bare YAML projects must work without --lang)", got)
	}
}

func TestResolveDefaultsToTypeScript(t *testing.T) {
	dir := t.TempDir()

	got, err := language.Resolve("", "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != language.TypeScript {
		t.Errorf("Resolve = %q, want typescript default", got)
	}
}

func TestResolveInvalidFlag(t *testing.T) {
	if _, err := language.Resolve("kotlin", "yaml", ""); !errors.Is(err, language.ErrLanguageUnknown) {
		t.Errorf("Resolve = %v, want ErrLanguageUnknown", err)
	}
}
