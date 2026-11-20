package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/data"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

const (
	// maxSignalSchemaBytes bounds one schema document so an untrusted wait
	// schema cannot make validation allocate unbounded memory.
	maxSignalSchemaBytes = 64 << 10
	// maxSignalSchemaDepth bounds nested properties so a deeply nested schema
	// cannot exhaust the stack.
	maxSignalSchemaDepth = 16
	// maxSignalSchemaKeywords bounds the number of supported keywords applied
	// at one schema level (required names and declared properties).
	maxSignalSchemaKeywords = 256
)

// SignalDeliveryStatus is the outcome returned to the original sender and
// retained with the delivery record.
type SignalDeliveryStatus string

const (
	// SignalDeliveryAccepted means the delivery matched a waiting capability
	// and was committed with its resolution.
	SignalDeliveryAccepted SignalDeliveryStatus = "accepted"
)

// SignalDelivery is the durable record of one accepted signal delivery. It is
// stored at SignalDeliveryKey and exists only for matched attempts; an
// unmatched delivery is not recorded so a later retry can still succeed.
// PayloadHash binds an idempotent replay to the original payload without
// retaining the payload itself.
type SignalDelivery struct {
	DeliveryID     string               `json:"delivery_id"`
	ExecutionID    shared.ID            `json:"execution_id"`
	SignalName     string               `json:"signal_name"`
	CorrelationKey string               `json:"correlation_key,omitempty"`
	CapabilityID   shared.ID            `json:"capability_id"`
	PayloadHash    string               `json:"payload_hash"`
	Status         SignalDeliveryStatus `json:"status"`
	CreatedAt      time.Time            `json:"created_at"`
}

// SignalPayloadHash is the single authority for hashing a signal payload. It
// canonicalizes the object before hashing so key order cannot change the
// identity of an otherwise identical delivery. NewSignalDelivery and the
// scheduler's replay check both call it, so there is only one hashing policy.
func SignalPayloadHash(payload map[string]any) (string, error) {
	encoded, err := json.Marshal(data.CanonicalMap(payload))
	if err != nil {
		return "", fmt.Errorf("marshal signal payload: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// NewSignalDelivery builds a delivery record and binds it to the hash of its
// payload for duplicate detection. Validation happens here so the persistence
// boundary never has to guess what identifies a delivery.
func NewSignalDelivery(executionID, capabilityID shared.ID, deliveryID, signalName, correlationKey string, payload map[string]any) (*SignalDelivery, error) {
	if executionID == "" || capabilityID == "" {
		return nil, fmt.Errorf("signal delivery requires execution and capability IDs")
	}
	if deliveryID == "" {
		return nil, fmt.Errorf("signal delivery id is required")
	}
	if signalName == "" {
		return nil, fmt.Errorf("signal name is required")
	}
	hash, err := SignalPayloadHash(payload)
	if err != nil {
		return nil, err
	}
	return &SignalDelivery{
		DeliveryID:     deliveryID,
		ExecutionID:    executionID,
		SignalName:     signalName,
		CorrelationKey: correlationKey,
		CapabilityID:   capabilityID,
		PayloadHash:    hash,
		Status:         SignalDeliveryAccepted,
	}, nil
}

// SignalDeliveryKey returns the durable key for a delivery record. Key
// validation rejects traversal segments in deliveryID.
func SignalDeliveryKey(executionID shared.ID, deliveryID string) (string, error) {
	if deliveryID == "" {
		return "", fmt.Errorf("signal delivery id is required")
	}
	return storage.Key("signals", string(executionID), deliveryID)
}

// ValidateSignalPayload checks a delivered object against a wait's
// PayloadSchema using a bounded, stdlib-only subset of JSON Schema object
// keywords: type, required, properties, and additionalProperties.
//
// This is deliberately not a full JSON Schema implementation. An empty schema
// accepts any object. An opaque (non-JSON) schema name and a parsed schema with
// none of the supported keywords are treated as compatibility labels: any
// object payload is accepted, preserving the earlier descriptive-only meaning
// of PayloadSchema. Unsupported keywords are ignored. Schemas larger than
// maxSignalSchemaBytes or nested deeper than maxSignalSchemaDepth are rejected
// rather than processed without bound.
func ValidateSignalPayload(schema string, payload map[string]any) error {
	if schema == "" {
		return nil
	}
	if len(schema) > maxSignalSchemaBytes {
		return fmt.Errorf("signal payload schema exceeds %d bytes", maxSignalSchemaBytes)
	}
	var parsed any
	if err := json.Unmarshal([]byte(schema), &parsed); err != nil {
		// Opaque schema name: compatibility mode, accept any object.
		return nil
	}
	return validateSignalValue(parsed, payload, 0)
}

// validateSignalValue applies the supported object-keyword subset of schema to
// value. Only object schemas carry supported keywords; scalar or boolean
// schemas fall back to compatibility. Schema parsing is bounded by depth and
// keyword count so an untrusted document cannot exhaust resources.
func validateSignalValue(schema any, value any, depth int) error {
	if depth > maxSignalSchemaDepth {
		return fmt.Errorf("signal payload schema nesting exceeds %d levels", maxSignalSchemaDepth)
	}
	object, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	payload, isObject := value.(map[string]any)

	if raw, present := object["type"]; present {
		if err := validateSignalType(raw, value, isObject); err != nil {
			return err
		}
	}
	if !isObject {
		return nil
	}

	properties, _ := object["properties"].(map[string]any)
	if len(properties) > maxSignalSchemaKeywords {
		return fmt.Errorf("signal payload schema declares too many properties")
	}

	if raw, present := object["required"]; present {
		names, ok := raw.([]any)
		if !ok || len(names) > maxSignalSchemaKeywords {
			return fmt.Errorf("signal payload schema has an invalid required list")
		}
		for _, item := range names {
			name, ok := item.(string)
			if !ok {
				return fmt.Errorf("signal payload schema has a non-string required name")
			}
			if _, exists := payload[name]; !exists {
				return fmt.Errorf("signal payload is missing required field %q", name)
			}
		}
	}

	for name, subschema := range properties {
		field, exists := payload[name]
		if !exists {
			continue
		}
		if err := validateSignalValue(subschema, field, depth+1); err != nil {
			return fmt.Errorf("signal payload field %q: %w", name, err)
		}
	}

	if raw, present := object["additionalProperties"]; present {
		switch policy := raw.(type) {
		case bool:
			if !policy {
				for name := range payload {
					if _, declared := properties[name]; !declared {
						return fmt.Errorf("signal payload has undeclared field %q", name)
					}
				}
			}
		case map[string]any:
			for name, field := range payload {
				if _, declared := properties[name]; declared {
					continue
				}
				if err := validateSignalValue(policy, field, depth+1); err != nil {
					return fmt.Errorf("signal payload additional field %q: %w", name, err)
				}
			}
		}
	}
	return nil
}

// validateSignalType enforces the supported single-type and union-type forms.
// Unknown type names are ignored because this is a bounded subset, not full
// JSON Schema.
func validateSignalType(raw any, value any, isObject bool) error {
	switch expected := raw.(type) {
	case string:
		if knownSignalType(expected) && !signalValueMatches(expected, value, isObject) {
			return fmt.Errorf("signal payload of type %T does not match schema type %q", value, expected)
		}
	case []any:
		known := false
		for _, candidate := range expected {
			name, ok := candidate.(string)
			if !ok || !knownSignalType(name) {
				continue
			}
			known = true
			if signalValueMatches(name, value, isObject) {
				return nil
			}
		}
		if known {
			return fmt.Errorf("signal payload of type %T matches none of the schema types %v", value, expected)
		}
	}
	return nil
}

func knownSignalType(name string) bool {
	switch name {
	case "object", "string", "boolean", "number", "integer", "array", "null":
		return true
	default:
		return false
	}
}

func signalValueMatches(name string, value any, isObject bool) bool {
	switch name {
	case "object":
		return isObject
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number", "integer":
		switch value.(type) {
		case float64, json.Number:
			return true
		default:
			return false
		}
	case "array":
		_, ok := value.([]any)
		return ok
	case "null":
		return value == nil
	default:
		return true
	}
}
