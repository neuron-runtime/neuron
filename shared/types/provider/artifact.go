package provider

import (
	"fmt"
	"strings"
)

// InstallRecord is written into an installed provider directory after a verified,
// atomic install. It records the exact artifact identity, digest, and provenance.
type InstallRecord struct {
	Provider     string            `json:"provider"`
	Version      string            `json:"version"`
	Digest       string            `json:"digest"`
	ArchiveName  string            `json:"archiveName,omitempty"`
	InstalledAt  int64             `json:"installedAt,omitempty"`
	Registry     string            `json:"registry,omitempty"`
	ResolvedRef  string            `json:"resolvedRef,omitempty"`
	Source       string            `json:"source,omitempty"`
	ManifestHash string            `json:"manifestHash,omitempty"`
	Extras       map[string]string `json:"extras,omitempty"`
}

// ProviderTypePath returns the path segment for a provider type.
func ProviderTypePath(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("provider name is required")
	}
	clean := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(name), ":", "-"), "/", "-")
	if clean == "" {
		return "", fmt.Errorf("provider name %q cannot be sanitized", name)
	}
	return clean, nil
}
