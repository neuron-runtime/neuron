package protocol

import "time"

// AssemblyResponse describes one registered Assembly for discovery and
// metadata views. It is the public shape a client uses to list the Assemblies a
// runtime hosts and to inspect one before running it; it deliberately exposes
// identity and registration time, not the compiled graph.
type AssemblyResponse struct {
	AssemblyID   string    `json:"assembly_id"`
	Version      string    `json:"version,omitempty"`
	Hash         string    `json:"hash,omitempty"`
	Env          string    `json:"env,omitempty"`
	RegisteredAt time.Time `json:"registered_at,omitempty"`
}
