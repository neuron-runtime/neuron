package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
)

// InstallRecord is written into an installed provider directory after a
// verified, atomic install. It records the exact artifact identity, digest,
// and provenance. RootDir and ManifestPath are convenience locations inside
// the installed directory; they are not artifact identity — the identity is
// (provider, version, digest) — and must never be treated as such.
type InstallRecord struct {
	Provider     string    `json:"provider"`
	Version      string    `json:"version"`
	Digest       string    `json:"digest,omitempty"`
	Source       string    `json:"source,omitempty"`
	RootDir      string    `json:"rootDir"`
	ManifestPath string    `json:"manifestPath"`
	InstalledAt  time.Time `json:"installedAt"`
}

// WriteInstallRecord persists the record at path.
func WriteInstallRecord(path string, rec InstallRecord) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal provider install record: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// ReadInstallRecord loads the record at path.
func ReadInstallRecord(path string) (*InstallRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rec InstallRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("parse provider install record %s: %w", path, err)
	}
	return &rec, nil
}

// ProviderTypePath returns the path segment for a provider name inside the
// store. The sanitization rule matches the one shared/types/provider uses for
// canonical archive names, so a name maps to the same segment in both
// layouts; both rules are pinned by tests so they cannot drift silently.
func ProviderTypePath(name string) (string, error) {
	clean := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(name), ":", "-"), "/", "-")
	if clean == "" {
		return "", fmt.Errorf("provider name is required")
	}
	return clean, nil
}

// ManifestFile, InstallFile match the shared contract constants.
const (
	ManifestFile = providertypes.ManifestFile
	InstallFile  = providertypes.InstallFile
)
