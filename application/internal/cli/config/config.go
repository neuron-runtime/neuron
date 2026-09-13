package config

import "fmt"

const NeuronConfigFileName = "neuron.config.yaml"

// NeuronConfigDefaultTemplate returns the starter project configuration for a
// new Neuron project named name.
//
// The template follows the modern neuron.config.* surface: the project
// configuration is the single source of truth for the authoring language, the
// System entry file, and runtime defaults. The default entry point is a
// `kind: System` file at <root>/system.yaml (see project.ResolveSystem).
// Runtime internals (storage, executor store directory) are managed by Neuron
// and rejected from configuration files.
func NeuronConfigDefaultTemplate(name string) string {
	return fmt.Sprintf(`#
# Neuron project configuration. neuron.config.json | .yaml | .yml is the single
# source of truth for how this project (name: %s) is authored and runs.
#

lang: yaml
entry: system.yaml

runtime:
  execution:
    mode: wait
    timeout: 30m
  workers:
    min: 1
    max: 8

# External executors are resolved against the registries listed here. Without a
# registry block, only built-in executors (neuron:core:*) are available.
executors:
  registries:
    - name: local
      url: ./executors

inspector:
  enabled: true
  address: 127.0.0.1:7433
`, name)
}
