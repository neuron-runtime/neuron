package execution

import (
	"strings"
	"testing"
)

func TestSignalPayloadHashIsSingleAuthority(t *testing.T) {
	first, err := SignalPayloadHash(map[string]any{"a": 1, "b": 2})
	if err != nil {
		t.Fatalf("SignalPayloadHash: %v", err)
	}
	second, err := SignalPayloadHash(map[string]any{"b": 2, "a": 1})
	if err != nil {
		t.Fatalf("SignalPayloadHash: %v", err)
	}
	if first != second {
		t.Fatalf("hash depends on key order: %s != %s", first, second)
	}
	changed, err := SignalPayloadHash(map[string]any{"a": 2, "b": 2})
	if err != nil {
		t.Fatalf("SignalPayloadHash: %v", err)
	}
	if first == changed {
		t.Fatal("different payloads produced the same hash")
	}

	delivery, err := NewSignalDelivery("exec", "cap", "del", "approved", "", map[string]any{"a": 1, "b": 2})
	if err != nil {
		t.Fatalf("NewSignalDelivery: %v", err)
	}
	if delivery.PayloadHash != first {
		t.Fatalf("NewSignalDelivery hash = %s, want the SignalPayloadHash result %s", delivery.PayloadHash, first)
	}
}

func TestValidateSignalPayload(t *testing.T) {
	cases := []struct {
		name    string
		schema  string
		payload map[string]any
		wantErr string
	}{
		{
			name:    "empty schema accepts any object",
			schema:  "",
			payload: map[string]any{"anything": 1},
		},
		{
			name:    "opaque name accepts any object",
			schema:  "approval-schema-v1",
			payload: map[string]any{"anything": 1},
		},
		{
			name:    "parsed object without supported keywords is a compatibility label",
			schema:  `{"$schema":"http://json-schema.org/draft-07/schema#","description":"info only"}`,
			payload: map[string]any{"anything": 1},
		},
		{
			name:    "required field present",
			schema:  `{"required":["status"]}`,
			payload: map[string]any{"status": "approved"},
		},
		{
			name:    "required field missing",
			schema:  `{"required":["status"]}`,
			payload: map[string]any{"ok": true},
			wantErr: `missing required field "status"`,
		},
		{
			name:    "supported keywords enforce a typed object",
			schema:  `{"type":"object","required":["status"],"properties":{"status":{"type":"string"}}}`,
			payload: map[string]any{"status": "approved"},
		},
		{
			name:    "property type enforced",
			schema:  `{"properties":{"count":{"type":"integer"}}}`,
			payload: map[string]any{"count": "not-a-number"},
			wantErr: `field "count"`,
		},
		{
			name:    "nested object validated",
			schema:  `{"properties":{"user":{"type":"object","required":["id"]}}}`,
			payload: map[string]any{"user": map[string]any{"name": "x"}},
			wantErr: `missing required field "id"`,
		},
		{
			name:    "additionalProperties false rejects undeclared field",
			schema:  `{"properties":{"status":{}},"additionalProperties":false}`,
			payload: map[string]any{"status": "ok", "extra": 1},
			wantErr: `undeclared field "extra"`,
		},
		{
			name:    "additionalProperties false accepts declared fields",
			schema:  `{"properties":{"status":{}},"additionalProperties":false}`,
			payload: map[string]any{"status": "ok"},
		},
		{
			name:    "additionalProperties schema validates extra field",
			schema:  `{"properties":{"status":{}},"additionalProperties":{"type":"boolean"}}`,
			payload: map[string]any{"status": "ok", "extra": "not-bool"},
			wantErr: `additional field "extra"`,
		},
		{
			name:    "union type accepts a matching member",
			schema:  `{"properties":{"value":{"type":["string","integer"]}}}`,
			payload: map[string]any{"value": "text"},
		},
		{
			name:    "union type rejects all members",
			schema:  `{"properties":{"value":{"type":["string","integer"]}}}`,
			payload: map[string]any{"value": true},
			wantErr: `field "value"`,
		},
		{
			name:    "unsupported keyword is ignored",
			schema:  `{"required":["status"],"minProperties":99}`,
			payload: map[string]any{"status": "ok"},
		},
		{
			name:    "malformed JSON is treated as an opaque name",
			schema:  `{"required":[`,
			payload: map[string]any{"anything": 1},
		},
		{
			name:    "non-object top-level JSON is ignored",
			schema:  `true`,
			payload: map[string]any{"anything": 1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSignalPayload(tc.schema, tc.payload)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateSignalPayload: unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateSignalPayload error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateSignalPayloadIsBounded(t *testing.T) {
	oversized := `{"description":"` + strings.Repeat("x", maxSignalSchemaBytes) + `"}`
	if err := ValidateSignalPayload(oversized, map[string]any{}); err == nil {
		t.Fatal("oversized schema was accepted, want rejection")
	}

	// Mirror the schema nesting in the payload so validation actually recurses;
	// an empty payload would short-circuit before the depth bound is reached.
	schema := `{"required":["leaf"]}`
	var payload any = map[string]any{"leaf": true}
	for i := 0; i < maxSignalSchemaDepth+3; i++ {
		schema = `{"properties":{"next":` + schema + `}}`
		payload = map[string]any{"next": payload}
	}
	if err := ValidateSignalPayload(schema, payload.(map[string]any)); err == nil {
		t.Fatal("over-deep schema was accepted, want rejection")
	}
}
