package protocol

import (
	"github.com/Muhammad-Jay/neuron/shared/types/core"
)

// RegisterRequest registers an assembly definition durably with N.O.R.E. It
// does not create or start an Instance; instances are created lazily on the
// first execution request for the assembly's key.
type RegisterRequest struct {
	Assembly core.Assembly `json:"assembly"`

	// Key is the client-computed identity (AssemblyID, Version, Hash, Env). When
	// provided the server validates Hash matches the assembly content; when
	// empty the server derives the key from the assembly metadata.
	Key InstanceKey `json:"key,omitempty"`

	// ExecutionConfigurations carries opaque capability-runtime-requirement
	// metadata that N.O.R.E. persists alongside the assembly for later runtime
	// assembly.
	ExecutionConfigurations any `json:"execution_configurations,omitempty"`

	// Force clears the registered assembly for the resolved name:version — and
	// removes any instances built from it — before registering the new one.
	Force bool `json:"force,omitempty"`
}

// RegisterStatus describes the outcome of a registration against a durable key.
type RegisterStatus string

const (
	RegisterStatusRegistered        RegisterStatus = "registered"
	RegisterStatusAlreadyRegistered RegisterStatus = "already_registered"
	RegisterStatusReplaced          RegisterStatus = "replaced"
)

type RegisterResponse struct {
	Key     InstanceKey    `json:"key"`
	Status  RegisterStatus `json:"status"`
	Message string         `json:"message,omitempty"`
}
