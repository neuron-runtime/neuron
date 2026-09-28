package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	neuronDirectory     = ".neuron"
	registrationKeyFile = "register.json"
)

// RegistrationKeyPath returns the path where the last successfully registered
// assembly key is persisted. It lives in the project's .neuron directory.
func RegistrationKeyPath(projectRoot string) string {
	return filepath.Join(
		projectRoot,
		neuronDirectory,
		registrationKeyFile,
	)
}

// SaveRegistrationKey persists the key reported by a successful registration so
// run-only flows (e.g. `neuron run`) can address the registered assembly without
// re-building or re-parsing the project.
func SaveRegistrationKey(projectRoot string, key any) error {
	if key == nil {
		return fmt.Errorf("registration key is nil")
	}
	path := RegistrationKeyPath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create .neuron directory: %w", err)
	}
	data, err := json.MarshalIndent(key, "", "  ")
	if err != nil {
		return fmt.Errorf("encode registration key: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write registration key: %w", err)
	}
	return nil
}

// LoadRegistrationKey reads the key persisted by SaveRegistrationKey.
func LoadRegistrationKey(projectRoot string, out any) error {
	if out == nil {
		return fmt.Errorf("registration key destination is nil")
	}
	data, err := os.ReadFile(RegistrationKeyPath(projectRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotRegistered
		}
		return fmt.Errorf("read registration key: %w", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode registration key: %w", err)
	}
	return nil
}
