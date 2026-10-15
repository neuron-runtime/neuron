package provider

import (
	"fmt"
	"strings"
	"time"
)

// InstallRecord is written into an installed provider directory after a verified,
// atomic install. It records the exact artifact identity, digest, and provenance.
type InstallRecord struct {
	Provider     string            `json:"provider"`
	Version      string            `json:"version"`
	Digest       string            `json:"digest"`
	ArchiveName  string            `json:"archiveName,omitempty"`
	InstalledAt  time.Time         `json:"installedAt,omitempty"`
	Registry     string            `json:"registry,omitempty"`
	ResolvedRef  string            `json:"resolvedRef,omitempty"`
	Source       string            `json:"source,omitempty"`
	ManifestHash string            `json:"manifestHash,omitempty"`
	Extras       map[string]string `json:"extras,omitempty"`
}

// ProviderTypePath returns the path segment for a provider type.
func ProviderTypePath(name string) (string, error) {
	clean := sanitizeName(name)
	if clean == "" {
		return "", fmt.Errorf("provider name is required")
	}
	return clean, nil
}

// sanitizeName reduces a provider name to a filesystem- and archive-safe
// segment by replacing the separators that appear in provider references. It is
// the single sanitization rule shared by archive names and store paths.
func sanitizeName(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(name), ":", "-"), "/", "-")
}
