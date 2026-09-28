package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const capabilityRuntimesFile = "capabilityRuntimes.json"

// CapabilityRuntimePin pins one resolved capability runtime requirement into a project. `neuron
// add` writes these so a developer can record the exact version of an capability runtime
// a project depends on, independently of the installed-artifact store.
type CapabilityRuntimePin struct {
	// Type is the capability runtime logical name (e.g. github:read).
	Type string `json:"type"`

	// Version is the exact installed version that satisfied the requirement.
	Version string `json:"version"`

	// Registry is the registry the artifact was obtained from.
	Registry string `json:"registry,omitempty"`

	// Digest is the verified artifact digest recorded at install time.
	Digest string `json:"digest,omitempty"`
}

// CapabilityRuntimesFile is the project-pinned capability runtime requirement record persisted to
// .neuron/capabilityRuntimes.json. Pins are ordered by insertion; a type appears once.
type CapabilityRuntimesFile struct {
	Pins []CapabilityRuntimePin `json:"pins"`
}

// CapabilityRuntimesFilePath returns the path of the project's pinned-capabilityRuntime record
// inside its .neuron directory.
func CapabilityRuntimesFilePath(projectRoot string) string {
	return filepath.Join(projectRoot, neuronDirectory, capabilityRuntimesFile)
}

// LoadCapabilityRuntimesFile reads the pinned-capabilityRuntime record written by SaveCapabilityRuntimesFile.
// A missing record is not an error; it yields an empty file.
func LoadCapabilityRuntimesFile(projectRoot string) (CapabilityRuntimesFile, error) {
	data, err := os.ReadFile(CapabilityRuntimesFilePath(projectRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return CapabilityRuntimesFile{}, nil
		}
		return CapabilityRuntimesFile{}, fmt.Errorf("read pinned capability runtimes: %w", err)
	}
	var file CapabilityRuntimesFile
	if err := json.Unmarshal(data, &file); err != nil {
		return CapabilityRuntimesFile{}, fmt.Errorf("decode pinned capability runtimes: %w", err)
	}
	if file.Pins == nil {
		file.Pins = []CapabilityRuntimePin{}
	}
	return file, nil
}

// SaveCapabilityRuntimesFile persists a pinned-capabilityRuntime record for projectRoot.
func SaveCapabilityRuntimesFile(projectRoot string, file CapabilityRuntimesFile) error {
	if file.Pins == nil {
		file.Pins = []CapabilityRuntimePin{}
	}
	path := CapabilityRuntimesFilePath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create .neuron directory: %w", err)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pinned capability runtimes: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write pinned capability runtimes: %w", err)
	}
	return nil
}

// Upsert pins pin, replacing any existing pin for the same type or appending a
// new entry when the type is not pinned yet.
func (f *CapabilityRuntimesFile) Upsert(pin CapabilityRuntimePin) {
	for i := range f.Pins {
		if f.Pins[i].Type == pin.Type {
			f.Pins[i] = pin
			return
		}
	}
	f.Pins = append(f.Pins, pin)
}
