package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
)

// InstallRecord is written into an installed provider directory after a verified,
// atomic install. It records the exact artifact identity, digest, and provenance.
type InstallRecord struct {
	Provider     string      `json:"provider"`
	Version      string      `json:"version"`
	Digest       string      `json:"digest,omitempty"`
	Registry     string      `json:"registry,omitempty"`
	Platform     string      `json:"platform,omitempty"`
	RootDir      string      `json:"rootDir"`
	ManifestPath string      `json:"manifestPath"`
	InstalledAt  time.Time   `json:"installedAt"`
	Manifest     interface{} `json:"manifest,omitempty"`
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

// ProviderTypePath returns the path segment for a provider type.
func ProviderTypePath(name string) (string, error) {
	clean := sanitizeName(name)
	if clean == "" {
		return "", fmt.Errorf("provider name is required")
	}
	return clean, nil
}

func sanitizeName(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(name), ":", "-"), "/", "-")
}

// PackageArchiveName returns the canonical archive name for a provider.
func PackageArchiveName(name, version string) string {
	return providertypes.PackageArchiveName(name, version)
}

// ManifestFile, InstallFile, APIVersion etc match shared contract constants.
const (
	ManifestFile = providertypes.ManifestFile
	InstallFile  = providertypes.InstallFile
)
