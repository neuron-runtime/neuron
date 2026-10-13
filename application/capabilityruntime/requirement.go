package capabilityruntime

import (
	"fmt"
	"strings"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// Requirement is what a Assembly asks for: a logical capability runtime type, an optional
// version constraint, and the registries allowed to satisfy it.
//
//	type:    "github:read"
//	version: "^1.0.0"
//	registries: ["github"]
//
// A Requirement is a request, not an installation. Resolution turns it into an
// immutable Package, and installation turns that Package into an Installed
// capabilityruntime. N.O.R.E. never stores Requirements as resolved capability runtimes.
type Requirement struct {
	Type       string   `json:"type"`
	Version    string   `json:"version,omitempty"`
	Registries []string `json:"registries,omitempty"`
}

// Validate verifies a Requirement is addressable.
func (r Requirement) Validate() error {
	if strings.TrimSpace(r.Type) == "" {
		return fmt.Errorf("capability runtime requirement has no type")
	}
	if _, err := capabilityrt.ParseType(r.Type); err != nil {
		return err
	}
	return nil
}
