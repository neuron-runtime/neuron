package project

import (
	"fmt"
	"strings"
)

func validateSystemBasic(
	system SystemFile,
) error {

	var errors []string

	if system.APIVersion == "" {
		errors = append(errors, "apiVersion is required")
	}

	if system.Kind != "System" {
		errors = append(
			errors,
			fmt.Sprintf(
				"kind must be System, got %q",
				system.Kind,
			),
		)
	}

	if strings.TrimSpace(system.Metadata.Name) == "" {
		errors = append(errors, "metadata.name is required")
	}

	if strings.TrimSpace(system.Metadata.Version) == "" {
		errors = append(errors, "metadata.version is required")
	}

	if len(system.Services) == 0 {
		errors = append(
			errors,
			"systems.services must contain at least one service",
		)
	}

	if len(system.Connectors) > 0 {
		serviceRefs := make(map[string]bool)
		for _, svc := range system.Services {
			serviceRefs[svc.Ref] = true
		}
		for i, conn := range system.Connectors {
			if err := validateConnectorBasic(conn, serviceRefs, i); err != nil {
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

func validateConnectorBasic(
	conn ConnectorReference,
	serviceRefs map[string]bool,
	index int,
) error {
	prefix := fmt.Sprintf("connectors[%d]", index)

	if conn.Entry == "" {
		if strings.TrimSpace(conn.From) == "" {
			return fmt.Errorf("%s.from is required", prefix)
		}
		if strings.TrimSpace(conn.To) == "" {
			return fmt.Errorf("%s.to is required", prefix)
		}
		if !serviceRefs[conn.From] {
			return fmt.Errorf("%s.from references unknown service %q", prefix, conn.From)
		}
		if !serviceRefs[conn.To] {
			return fmt.Errorf("%s.to references unknown service %q", prefix, conn.To)
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

// validateConnectorFile validates a ConnectorFile (for external connector files)
func validateConnectorFile(conn ConnectorFile) error {
	if strings.TrimSpace(conn.From) == "" {
		return fmt.Errorf("connector.from is required")
	}
	if strings.TrimSpace(conn.To) == "" {
		return fmt.Errorf("connector.to is required")
	}

	for j, m := range conn.Mappings {
		if strings.TrimSpace(m.Target) == "" {
			return fmt.Errorf("connector.mappings[%d].target is required", j)
		}
		if strings.TrimSpace(m.Expression) == "" {
			return fmt.Errorf("connector.mappings[%d].expression is required", j)
		}
	}

	for j, v := range conn.Validations {
		if strings.TrimSpace(v.Expression) == "" {
			return fmt.Errorf("connector.validations[%d].expression is required", j)
		}
	}

	return nil
}

func validateServiceBasic(
	service ServiceFile,
) error {

	var errors []string

	if service.APIVersion == "" {
		errors = append(errors, "apiVersion is required")
	}

	if service.Kind != "Service" {
		errors = append(
			errors,
			fmt.Sprintf(
				"kind must be Service, got %q",
				service.Kind,
			),
		)
	}

	if strings.TrimSpace(service.Metadata.Name) == "" {
		errors = append(errors, "metadata.name is required")
	}

	if strings.TrimSpace(service.Metadata.Version) == "" {
		errors = append(errors, "metadata.version is required")
	}

	if strings.TrimSpace(service.Spec.Executor.Type) == "" {
		errors = append(
			errors,
			"spec.executor.type is required",
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
