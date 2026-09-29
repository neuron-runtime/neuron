package capabilityruntime

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// InstallRecord is the install.json persisted inside every installed capability runtime
// directory. It is the debugging and audit trail for an immutable artifact.
type InstallRecord struct {
	Type         string      `json:"type"`
	Version      string      `json:"version"`
	Digest       string      `json:"digest,omitempty"`
	Registry     string      `json:"registry,omitempty"`
	Platform     string      `json:"platform,omitempty"`
	RootDir      string      `json:"rootDir"`
	ManifestPath string      `json:"manifestPath"`
	ArtifactPath string      `json:"artifactPath,omitempty"`
	Runtime      RuntimeSpec `json:"runtime"`
	Capabilities []string    `json:"capabilities,omitempty"`
	Features     []string    `json:"features,omitempty"`
	InstalledAt  time.Time   `json:"installedAt"`
}

// RecordFor builds an install record from an installed capabilityruntime. It preserves
// the runtime contract fields needed to launch the artifact later.
func RecordFor(i *Installed) InstallRecord {
	return InstallRecord{
		Type:         i.Type,
		Version:      i.Version,
		Digest:       i.Digest,
		Registry:     i.Registry,
		Platform:     i.Platform,
		RootDir:      i.RootDir,
		ManifestPath: i.ManifestPath,
		ArtifactPath: i.ArtifactPath,
		Runtime:      i.Runtime,
		Capabilities: i.Capabilities,
		Features:     i.Features,
		InstalledAt:  time.Now().UTC(),
	}
}

// Installed converts the record into the capabilityruntime.Installed view.
func (r *InstallRecord) Installed() *Installed {
	return &Installed{
		Type:         r.Type,
		Version:      r.Version,
		Digest:       r.Digest,
		Registry:     r.Registry,
		Platform:     r.Platform,
		RootDir:      r.RootDir,
		ManifestPath: r.ManifestPath,
		ArtifactPath: r.ArtifactPath,
		Runtime:      r.Runtime,
		Capabilities: r.Capabilities,
		Features:     r.Features,
	}
}

// WriteInstallRecord persists the record at path.
func WriteInstallRecord(path string, rec InstallRecord) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal install record: %w", err)
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
		return nil, fmt.Errorf("parse install record %s: %w", path, err)
	}
	return &rec, nil
}

// Store-facing manifest and install file names.
const (
	ManifestFile = capabilityrt.ManifestFile
	InstallFile  = capabilityrt.InstallFile
)
