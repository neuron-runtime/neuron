package project

import (
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"

	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// UnmarshalYAML decodes a capability runtime declaration, rejecting any key
// inside runtimeConfig that the canonical schema does not declare.
//
// runtimeConfig is the part of the authoring surface whose keys map directly onto
// what N.O.R.E. enforces, so an unknown key there is almost always an author
// believing they configured something the runtime will never honor — a resource
// limit, a misspelled field, a field that belongs somewhere else. Dropping it
// silently would turn a broken declaration into a successful build, so it is
// rejected instead.
func (s *CapabilityRuntimeSpec) UnmarshalYAML(value *yaml.Node) error {
	if err := validateRuntimeConfigKeys(value); err != nil {
		return err
	}
	// The alias avoids recursing back into this method while still decoding
	// every declared field normally.
	type declaration CapabilityRuntimeSpec
	return value.Decode((*declaration)(s))
}

// validateRuntimeConfigKeys reports the first unsupported key in a runtimeConfig
// subtree.
//
// The accepted keys are read from core.RuntimeConfig's own yaml tags rather than
// a second hand-written list, so adding a field to the schema can never leave
// this check behind. That is the whole point: the schema stays the single
// authority for what may be expressed in YAML.
func validateRuntimeConfigKeys(spec *yaml.Node) error {
	if spec.Kind != yaml.MappingNode {
		return nil
	}
	runtimeConfig := mappingValue(spec, "runtimeConfig")
	if runtimeConfig == nil || runtimeConfig.Kind != yaml.MappingNode {
		return nil
	}
	return checkMappingKeys(runtimeConfig, reflect.TypeOf(core.RuntimeConfig{}), "runtimeConfig")
}

func checkMappingKeys(node *yaml.Node, schema reflect.Type, path string) error {
	fields := yamlFieldNames(schema)
	for index := 0; index+1 < len(node.Content); index += 2 {
		key := node.Content[index].Value
		field, known := fields[key]
		if !known {
			return fmt.Errorf("%s.%s is not a supported option", path, key)
		}
		nested := dereference(field.Type)
		if node.Content[index+1].Kind == yaml.MappingNode && nested.Kind() == reflect.Struct {
			if err := checkMappingKeys(node.Content[index+1], nested, path+"."+key); err != nil {
				return err
			}
		}
	}
	return nil
}

// yamlFieldNames maps every key a struct accepts to its field, using the yaml
// tag as the source of truth and falling back to the lowercased field name when
// a tag omits one.
func yamlFieldNames(schema reflect.Type) map[string]reflect.StructField {
	fields := make(map[string]reflect.StructField, schema.NumField())
	for index := 0; index < schema.NumField(); index++ {
		field := schema.Field(index)
		name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		if name == "-" {
			continue
		}
		fields[name] = field
	}
	return fields
}

func dereference(schema reflect.Type) reflect.Type {
	for schema.Kind() == reflect.Pointer {
		schema = schema.Elem()
	}
	return schema
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}
