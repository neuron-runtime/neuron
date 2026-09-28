package project

import (
	"fmt"
	"strings"
)

func validateAssemblyBasic(
	assembly AssemblyFile,
) error {

	var errors []string

	if assembly.APIVersion == "" {
		errors = append(errors, "apiVersion is required")
	}

	if assembly.Kind != "Assembly" {
		errors = append(
			errors,
			fmt.Sprintf(
				"kind must be Assembly, got %q",
				assembly.Kind,
			),
		)
	}

	if strings.TrimSpace(assembly.Metadata.Name) == "" {
		errors = append(errors, "metadata.name is required")
	}

	if strings.TrimSpace(assembly.Metadata.Version) == "" {
		errors = append(errors, "metadata.version is required")
	}

	if len(assembly.Capabilities) == 0 {
		errors = append(
			errors,
			"assemblies.capabilities must contain at least one capability",
		)
	}

	if len(assembly.Bindings) > 0 {
		capabilityRefs := make(map[string]bool)
		for _, svc := range assembly.Capabilities {
			capabilityRefs[svc.Ref] = true
		}
		for i, conn := range assembly.Bindings {
			if err := validateBindingBasic(conn, capabilityRefs, i); err != nil {
				errors = append(errors, err.Error())
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf(
			"%s",
			strings.Join(errors, "; "),
		)
	}

	return nil
}

func validateBindingBasic(
	conn BindingReference,
	capabilityRefs map[string]bool,
	index int,
) error {
	prefix := fmt.Sprintf("bindings[%d]", index)

	if conn.Entry == "" {
		if strings.TrimSpace(conn.From) == "" {
			return fmt.Errorf("%s.from is required", prefix)
		}
		if strings.TrimSpace(conn.To) == "" {
			return fmt.Errorf("%s.to is required", prefix)
		}
		if !capabilityRefs[conn.From] {
			return fmt.Errorf("%s.from references unknown capability %q", prefix, conn.From)
		}
		if !capabilityRefs[conn.To] {
			return fmt.Errorf("%s.to references unknown capability %q", prefix, conn.To)
		}
	}

	for j, m := range conn.Mappings {
		if strings.TrimSpace(m.Target) == "" {
			return fmt.Errorf("%s.mappings[%d].target is required", prefix, j)
		}
		if strings.TrimSpace(m.Expression) == "" {
			return fmt.Errorf("%s.mappings[%d].expression is required", prefix, j)
		}
	}

	for j, v := range conn.Validations {
		if strings.TrimSpace(v.Expression) == "" {
			return fmt.Errorf("%s.validations[%d].expression is required", prefix, j)
		}
	}

	return nil
}

// validateBindingFile validates a BindingFile (for external binding files)
func validateBindingFile(conn BindingFile) error {
	if strings.TrimSpace(conn.From) == "" {
		return fmt.Errorf("binding.from is required")
	}
	if strings.TrimSpace(conn.To) == "" {
		return fmt.Errorf("binding.to is required")
	}

	for j, m := range conn.Mappings {
		if strings.TrimSpace(m.Target) == "" {
			return fmt.Errorf("binding.mappings[%d].target is required", j)
		}
		if strings.TrimSpace(m.Expression) == "" {
			return fmt.Errorf("binding.mappings[%d].expression is required", j)
		}
	}

	for j, v := range conn.Validations {
		if strings.TrimSpace(v.Expression) == "" {
			return fmt.Errorf("binding.validations[%d].expression is required", j)
		}
	}

	return nil
}

func validateCapabilityBasic(
	capability CapabilityFile,
) error {

	var errors []string

	if capability.APIVersion == "" {
		errors = append(errors, "apiVersion is required")
	}

	if capability.Kind != "Capability" {
		errors = append(
			errors,
			fmt.Sprintf(
				"kind must be Capability, got %q",
				capability.Kind,
			),
		)
	}

	if strings.TrimSpace(capability.Metadata.Name) == "" {
		errors = append(errors, "metadata.name is required")
	}

	if strings.TrimSpace(capability.Metadata.Version) == "" {
		errors = append(errors, "metadata.version is required")
	}

	if strings.TrimSpace(capability.Spec.CapabilityRuntime.Type) == "" {
		errors = append(
			errors,
			"spec.capabilityruntime.type is required",
		)
	}

	if len(errors) > 0 {
		return fmt.Errorf(
			"%s",
			strings.Join(errors, "; "),
		)
	}

	return nil
}
