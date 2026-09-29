// Package assembly holds the durable representation of a registered assembly:
// a first-class N.O.R.E. resource that exists independently of any live
// Instance. Instances are reconstructed from a RegisteredAssembly on demand.
package assembly

import (
	"time"

	shared "github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// RegisteredAssembly is the durable artifact persisted by /v1/register.
//
// This is the source of truth for a assembly's definition. An Instance is only
// a transient runtime built from it, so the registered artifact is reloadable
// and re-instantiable after a restart.
type RegisteredAssembly struct {
	Key protocol.InstanceKey `json:"key"`

	Assembly shared.Assembly `json:"assembly"`

	// ExecutionConfigurations is stored opaquely. For CLI-sourced assemblies it is
	// a []project.CapabilityRuntimeSource, which the runtime module cannot import.
	ExecutionConfigurations any `json:"execution_configurations,omitempty"`

	// BlueprintMetadata is the assembly-level metadata produced when the assembly
	// is compiled, kept so list views can surface graph metadata lazily.
	BlueprintMetadata shared.Metadata `json:"blueprint_metadata,omitempty"`

	RegisteredAt time.Time `json:"registered_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
