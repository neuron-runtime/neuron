package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Muhammad-Jay/neuron/shared/types/core"
)

type InstanceKey struct {
	AssemblyID string `json:"assembly_id"`
	Version    string `json:"version,omitempty"`
	Hash       string `json:"hash,omitempty"`
	Env        string `json:"env,omitempty"`
}

// VersionLatest is the pseudo-version that resolves to the most recently
// registered version of an assembly. It is used when a key omits the version
// or explicitly asks for "latest". Resolution is time-based: whatever version
// was registered last wins, regardless of semver ordering.
const VersionLatest = "latest"

func (k InstanceKey) String() string {
	return fmt.Sprintf("%s@%s#%s:%s", k.AssemblyID, k.Version, k.Hash, k.Env)
}

// ColonString renders the key in its colon-encoded wire form used in URL
// paths: assemblyID[:version[:hash[:env]]]. The version defaults to "latest",
// but an empty hash or environment is preserved as an empty segment so a
// partial key stays partial: the server then resolves the registration
// regardless of hash or environment instead of defaulting them itself.
func (k InstanceKey) ColonString() string {
	version := k.Version
	if version == "" {
		version = VersionLatest
	}
	return fmt.Sprintf("%s:%s:%s:%s", k.AssemblyID, version, k.Hash, k.Env)
}

// ParseKey parses a colon-encoded InstanceKey:
// assemblyID[:version[:hash[:env]]]. Missing version and hash default to
// latest and ""; an omitted env is left empty so the server can resolve the
// registration regardless of environment.
func ParseKey(s string) (InstanceKey, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return InstanceKey{}, fmt.Errorf("instance key is empty")
	}
	parts := strings.Split(s, ":")
	if len(parts) > 4 {
		return InstanceKey{}, fmt.Errorf("instance key %q has too many segments", s)
	}

	key := InstanceKey{
		AssemblyID: parts[0],
		Version:    VersionLatest,
	}
	if key.AssemblyID == "" {
		return InstanceKey{}, fmt.Errorf("instance key %q has no assembly id", s)
	}
	if len(parts) > 1 {
		key.Version = parts[1]
	}
	if len(parts) > 2 {
		key.Hash = parts[2]
	}
	if len(parts) > 3 {
		key.Env = parts[3]
	}
	return key, nil
}

// ParseUserKey accepts the human-facing forms of an instance key and
// normalizes them to an InstanceKey:
//
//   - colon form: name[:version[:hash[:env]]] (see ParseKey)
//   - at form:    name@version[#hash][:env]
//   - bare name:  latest version is assumed
//
// Instance IDs (inst_*) are NOT parsed here; callers that accept both should
// branch on the prefix before calling.
func ParseUserKey(s string) (InstanceKey, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return InstanceKey{}, fmt.Errorf("instance key is empty")
	}
	if !strings.Contains(s, "@") {
		return ParseKey(s)
	}

	name, rest, ok := strings.Cut(s, "@")
	if !ok || name == "" {
		return InstanceKey{}, fmt.Errorf("invalid instance key %q", s)
	}
	key := InstanceKey{AssemblyID: name, Version: VersionLatest}

	// rest = version[#hash][:env]
	if hashAt := strings.IndexByte(rest, '#'); hashAt >= 0 {
		key.Version = rest[:hashAt]
		hashEnv := rest[hashAt+1:]
		if envAt := strings.IndexByte(hashEnv, ':'); envAt >= 0 {
			key.Hash = hashEnv[:envAt]
			key.Env = hashEnv[envAt+1:]
		} else {
			key.Hash = hashEnv
		}
	} else if envAt := strings.IndexByte(rest, ':'); envAt >= 0 {
		key.Version = rest[:envAt]
		key.Env = rest[envAt+1:]
	} else {
		key.Version = rest
	}
	return key, nil
}

type CreateInstanceRequest struct {
	Key      InstanceKey    `json:"key"`
	Assembly *core.Assembly `json:"assemblies"`
}

type InstanceResponse struct {
	ID                string        `json:"id"`
	BlueprintMetadata core.Metadata `json:"blueprint_metadata"`
	Status            string        `json:"status"`
	AssemblyID        string        `json:"assembly_id"`
	Version           string        `json:"version,omitempty"`
	Hash              string        `json:"hash,omitempty"`
	Env               string        `json:"env,omitempty"`
}

type ExecuteRequest struct {
	// Params is the free-form data passed to the execution's entry
	// capabilities.
	Params map[string]any `json:"params,omitempty"`
	// Mode selects how the server responds. "detach" returns immediately with
	// the execution accepted (HTTP 202); anything else (empty or "wait") waits
	// for the execution to finish and returns its final result (HTTP 200).
	Mode string `json:"mode,omitempty"`
}

type ExecuteResponse struct {
	ExecutionID core.ID   `json:"execution_id"`
	InstanceID  string    `json:"instance_id"`
	Status      string    `json:"status"`
	Time        time.Time `json:"time"`
}

// ExecutionResult is returned by a wait-mode Execute request once the
// execution has reached a terminal state. Results aggregates every capability
// result keyed by capability ID.
type ExecutionResult struct {
	ExecutionID core.ID                   `json:"execution_id"`
	InstanceID  string                    `json:"instance_id"`
	Status      string                    `json:"status"`
	Error       string                    `json:"error,omitempty"`
	Results     map[string]map[string]any `json:"results,omitempty"`
}

func HashBlueprint(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
