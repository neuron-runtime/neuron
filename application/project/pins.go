package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const executorsFile = "executors.json"

// ExecutorPin pins one resolved executor requirement into a project. `neuron
// add` writes these so a developer can record the exact version of an executor
// a project depends on, independently of the installed-artifact store.
type ExecutorPin struct {
	// Type is the executor logical name (e.g. github:read).
	Type string `json:"type"`

	// Version is the exact installed version that satisfied the requirement.
	Version string `json:"version"`

	// Registry is the registry the artifact was obtained from.
	Registry string `json:"registry,omitempty"`

	// Digest is the verified artifact digest recorded at install time.
	Digest string `json:"digest,omitempty"`
}

// ExecutorsFile is the project-pinned executor requirement record persisted to
// .neuron/executors.json. Pins are ordered by insertion; a type appears once.
type ExecutorsFile struct {
	Pins []ExecutorPin `json:"pins"`
}

// ExecutorsFilePath returns the path of the project's pinned-executor record
// inside its .neuron directory.
func ExecutorsFilePath(projectRoot string) string {
	return filepath.Join(projectRoot, neuronDirectory, executorsFile)
}

// LoadExecutorsFile reads the pinned-executor record written by SaveExecutorsFile.
// A missing record is not an error; it yields an empty file.
func LoadExecutorsFile(projectRoot string) (ExecutorsFile, error) {
	data, err := os.ReadFile(ExecutorsFilePath(projectRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return ExecutorsFile{}, nil
		}
		return ExecutorsFile{}, fmt.Errorf("read pinned executors: %w", err)
	}
	var file ExecutorsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return ExecutorsFile{}, fmt.Errorf("decode pinned executors: %w", err)
	}
	if file.Pins == nil {
		file.Pins = []ExecutorPin{}
	}
	return file, nil
}

// SaveExecutorsFile persists a pinned-executor record for projectRoot.
func SaveExecutorsFile(projectRoot string, file ExecutorsFile) error {
	if file.Pins == nil {
		file.Pins = []ExecutorPin{}
	}
	path := ExecutorsFilePath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create .neuron directory: %w", err)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pinned executors: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write pinned executors: %w", err)
	}
	return nil
}

// Upsert pins pin, replacing any existing pin for the same type or appending a
// new entry when the type is not pinned yet.
func (f *ExecutorsFile) Upsert(pin ExecutorPin) {
	for i := range f.Pins {
		if f.Pins[i].Type == pin.Type {
			f.Pins[i] = pin
			return
		}
	}
	f.Pins = append(f.Pins, pin)
}