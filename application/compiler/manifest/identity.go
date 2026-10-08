package manifest

import (
	"fmt"
	"strings"
)

// ApplyIdentity makes project configuration the single source of the
// Assembly's identity. It validates the configured name and version
// against any values the authoring surface already declared, then writes
// them into the manifest metadata.
//
// Project configuration (neuron.config.*) is authoritative for name and
// version. An authored metadata value that disagrees with the configured
// one is a conflict, not an override: the build fails with a message
// naming both values and both sources so the author aligns or removes the
// authored value rather than shipping two competing identities. An
// authored value that matches, or is absent, is simply replaced by the
// configured value.
//
// Only Name and Version are applied. Metadata.Description stays authored:
// it is not part of the configuration-owned identity.
func ApplyIdentity(m *Assembly, name, version string) error {
	if m == nil {
		return fmt.Errorf("assembly manifest is nil")
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf(`assembly name is required: add "name" to neuron.config.* (project configuration)`)
	}
	if strings.TrimSpace(version) == "" {
		return fmt.Errorf(`assembly version is required: add "version" to neuron.config.* (project configuration)`)
	}
	if authored := m.Metadata.Name; authored != "" && authored != name {
		return fmt.Errorf(
			"assembly name conflict: neuron.config.* declares %q but the authored assembly declares %q; project configuration is authoritative — remove or align the authored value",
			name, authored,
		)
	}
	if authored := m.Metadata.Version; authored != "" && authored != version {
		return fmt.Errorf(
			"assembly version conflict: neuron.config.* declares %q but the authored assembly declares %q; project configuration is authoritative — remove or align the authored value",
			version, authored,
		)
	}
	m.Metadata.Name = name
	m.Metadata.Version = version
	return nil
}
