package plugin

import (
	"context"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// TestInstanceAdapterStoresIdentityAsPlainStrings proves the adapter normalizes
// domain IDs before handing them to a provider. The process worker reads these
// keys with a string type assertion, so a named core.ID would be dropped and
// the capability runtime would receive empty identity fields.
func TestInstanceAdapterStoresIdentityAsPlainStrings(t *testing.T) {
	instance := &recordingInstance{}
	adapter := &instanceAdapter{instance: instance, typ: "acme:echo"}

	_, err := adapter.Execute(context.Background(), contracts.ExecutionContext{
		AttemptID:          core.ID("attempt-42"),
		LogicalOperationID: core.ID("logical-7"),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if instance.ctx == nil {
		t.Fatal("the runtime received no context")
	}

	for _, tc := range []struct {
		key  string
		want string
	}{
		{"neuron:attempt_id", "attempt-42"},
		{"neuron:logical_operation_id", "logical-7"},
	} {
		stored := instance.ctx.Value(tc.key)
		got, ok := stored.(string)
		if !ok {
			t.Errorf("context value for %q has type %T, want plain string", tc.key, stored)
			continue
		}
		if got != tc.want {
			t.Errorf("context value for %q = %q, want %q", tc.key, got, tc.want)
		}
	}
}
