// Package language enumerates the authoring languages a Neuron project can be
// written in. A language identifies how a project's source is turned into the
// canonical .neuron/manifest.json. It is intentionally distinct from both the
// executor protocol and the manifest schema: adding a new authoring language
// must not require runtime changes.
package language

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Language is one canonical project authoring language.
type Language string

const (
	// YAML is the canonical identifier for the YAML project frontend. The
	// short alias "yml" normalizes to it.
	YAML Language = "yaml"

	// TypeScript is the canonical identifier for the TypeScript SDK project
	// frontend. The short alias "ts" normalizes to it.
	TypeScript Language = "typescript"
)

// sentinel errors for language resolution.
var ErrLanguageUnknown = fmt.Errorf("unknown project language")

// aliases maps every accepted authoring token to its canonical Language.
var aliases = map[string]Language{
	"yaml":       YAML,
	"yml":        YAML,
	"typescript": TypeScript,
	"ts":         TypeScript,
}

// String returns the canonical name.
func (l Language) String() string {
	return string(l)
}

// Is reports whether l equals the canonical form.
func (l Language) Is(candidate Language) bool {
	return l == candidate
}

// Normalize canonicalizes an author-provided language token. It accepts
// "yaml", "yml", "typescript", and "ts" (case-insensitive).
func Normalize(s string) (Language, error) {
	canonical, ok := aliases[strings.ToLower(strings.TrimSpace(s))]
	if !ok {
		return "", fmt.Errorf("%w: %q (supported: yaml, yml, typescript, ts)", ErrLanguageUnknown, s)
	}
	return canonical, nil
}

// Resolve determines the effective project language. The CLI flag wins;
// otherwise the project configuration value is used, then a marker-file
// heuristic (index.ts → TypeScript, system.yaml → YAML). If no marker is found
// the language defaults to TypeScript.
func Resolve(flagValue, configValue, projectDir string) (Language, error) {
	if strings.TrimSpace(flagValue) != "" {
		return Normalize(flagValue)
	}
	if strings.TrimSpace(configValue) != "" {
		return Normalize(configValue)
	}
	if detected, ok := detectFromProjectDir(projectDir); ok {
		return detected, nil
	}
	return TypeScript, nil
}

// detectFromProjectDir infers the authoring language from a single marker file.
// TypeScript is recognized by index.ts; YAML is recognized by system.yaml.
func detectFromProjectDir(projectDir string) (Language, bool) {
	if projectDir == "" {
		return "", false
	}
	if info, err := os.Stat(filepath.Join(projectDir, "index.ts")); err == nil && !info.IsDir() {
		return TypeScript, true
	}
	if info, err := os.Stat(filepath.Join(projectDir, "system.yaml")); err == nil && !info.IsDir() {
		return YAML, true
	}
	return "", false
}
