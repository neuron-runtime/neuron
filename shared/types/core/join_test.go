package core

import "testing"

func TestJoinPolicyEffectiveDefaultsToAll(t *testing.T) {
	if got := JoinPolicy("").Effective(); got != JoinPolicyAll {
		t.Fatalf("empty policy effective = %q, want %q", got, JoinPolicyAll)
	}
	if got := JoinPolicyAll.Effective(); got != JoinPolicyAll {
		t.Fatalf("all policy effective = %q, want %q", got, JoinPolicyAll)
	}
}

func TestJoinPolicyValidateRejectsUnsupported(t *testing.T) {
	if err := JoinPolicyAll.Validate(); err != nil {
		t.Fatalf("all policy must be valid: %v", err)
	}
	if err := JoinPolicy("").Validate(); err != nil {
		t.Fatalf("an unspecified policy must be valid and mean all: %v", err)
	}
	if err := JoinPolicy("any").Validate(); err == nil {
		t.Fatal("an unsupported join policy must be rejected")
	}
}
