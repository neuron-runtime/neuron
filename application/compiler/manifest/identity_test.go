package manifest

import "testing"

func TestApplyIdentityInjectsConfiguredIdentity(t *testing.T) {
	m := &Assembly{
		Metadata: Metadata{Description: "keeps the authored description"},
	}

	if err := ApplyIdentity(m, "orders", "2.1.0"); err != nil {
		t.Fatalf("ApplyIdentity: %v", err)
	}
	if m.Metadata.Name != "orders" {
		t.Errorf("name = %q, want orders", m.Metadata.Name)
	}
	if m.Metadata.Version != "2.1.0" {
		t.Errorf("version = %q, want 2.1.0", m.Metadata.Version)
	}
	if m.Metadata.Description != "keeps the authored description" {
		t.Errorf("description = %q, want the authored description untouched", m.Metadata.Description)
	}
}

func TestApplyIdentityAcceptsMatchingAuthoredMetadata(t *testing.T) {
	m := &Assembly{
		Metadata: Metadata{Name: "orders", Version: "2.1.0", Description: "authored"},
	}

	if err := ApplyIdentity(m, "orders", "2.1.0"); err != nil {
		t.Fatalf("ApplyIdentity: %v", err)
	}
	if m.Metadata.Name != "orders" || m.Metadata.Version != "2.1.0" {
		t.Errorf("metadata = %+v, want orders@2.1.0", m.Metadata)
	}
	if m.Metadata.Description != "authored" {
		t.Errorf("description = %q, want the authored description untouched", m.Metadata.Description)
	}
}

func TestApplyIdentityRequiresName(t *testing.T) {
	m := &Assembly{}

	err := ApplyIdentity(m, "", "1.0.0")
	if err == nil {
		t.Fatal("expected an error for an empty configured name")
	}
	const want = `assembly name is required: add "name" to neuron.config.* (project configuration)`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

func TestApplyIdentityRequiresVersion(t *testing.T) {
	m := &Assembly{}

	err := ApplyIdentity(m, "orders", "  ")
	if err == nil {
		t.Fatal("expected an error for an empty configured version")
	}
	const want = `assembly version is required: add "version" to neuron.config.* (project configuration)`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

func TestApplyIdentityRejectsNameConflict(t *testing.T) {
	m := &Assembly{Metadata: Metadata{Name: "authored-name"}}

	err := ApplyIdentity(m, "configured-name", "1.0.0")
	if err == nil {
		t.Fatal("expected a conflict error when the authored name differs")
	}
	const want = `assembly name conflict: neuron.config.* declares "configured-name" but the authored assembly declares "authored-name"; project configuration is authoritative — remove or align the authored value`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if m.Metadata.Name != "authored-name" {
		t.Errorf("name = %q, want the manifest left unchanged on conflict", m.Metadata.Name)
	}
}

func TestApplyIdentityRejectsVersionConflict(t *testing.T) {
	m := &Assembly{Metadata: Metadata{Version: "0.9.0"}}

	err := ApplyIdentity(m, "orders", "1.0.0")
	if err == nil {
		t.Fatal("expected a conflict error when the authored version differs")
	}
	const want = `assembly version conflict: neuron.config.* declares "1.0.0" but the authored assembly declares "0.9.0"; project configuration is authoritative — remove or align the authored value`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if m.Metadata.Version != "0.9.0" {
		t.Errorf("version = %q, want the manifest left unchanged on conflict", m.Metadata.Version)
	}
}
