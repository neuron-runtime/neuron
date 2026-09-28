package protocol

import "testing"

func TestParseUserKey(t *testing.T) {
	tests := []struct {
		in   string
		want InstanceKey
	}{
		{"order-processing", InstanceKey{AssemblyID: "order-processing", Version: VersionLatest}},
		{"order-processing@1.0.0", InstanceKey{AssemblyID: "order-processing", Version: "1.0.0"}},
		{"order-processing@1.0.0#abc123:dev",
			InstanceKey{AssemblyID: "order-processing", Version: "1.0.0", Hash: "abc123", Env: "dev"}},
		{"order-processing@1.0.0#abc123",
			InstanceKey{AssemblyID: "order-processing", Version: "1.0.0", Hash: "abc123"}},
		{"order-processing:2.0.0", InstanceKey{AssemblyID: "order-processing", Version: "2.0.0"}},
		{"order-processing:2.0.0:def456:prod",
			InstanceKey{AssemblyID: "order-processing", Version: "2.0.0", Hash: "def456", Env: "prod"}},
	}
	for _, tt := range tests {
		got, err := ParseUserKey(tt.in)
		if err != nil {
			t.Errorf("ParseUserKey(%q) unexpected error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseUserKey(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestParseUserKeyErrors(t *testing.T) {
	for _, in := range []string{"", "   ", "@1.0.0"} {
		if _, err := ParseUserKey(in); err == nil {
			t.Errorf("ParseUserKey(%q) expected error, got none", in)
		}
	}
}

func TestParseUserKeyColonStringRoundTrip(t *testing.T) {
	key := InstanceKey{AssemblyID: "sys", Version: "1.0.0", Hash: "h", Env: "development"}
	parsed, err := ParseUserKey(key.ColonString())
	if err != nil {
		t.Fatalf("parse colon string: %v", err)
	}
	if parsed != key {
		t.Errorf("round trip mismatch: %+v != %+v", parsed, key)
	}

	parsed, err = ParseUserKey(key.String())
	if err != nil {
		t.Fatalf("parse at-form: %v", err)
	}
	if parsed != key {
		t.Errorf("round trip mismatch: %+v != %+v", parsed, key)
	}
}

func TestColonStringPreservesPartialKey(t *testing.T) {
	tests := []struct {
		key  InstanceKey
		want string
	}{
		{InstanceKey{AssemblyID: "order-processing-ts", Version: "2.0.0"}, "order-processing-ts:2.0.0::"},
		{InstanceKey{AssemblyID: "order-processing-ts"}, "order-processing-ts:latest::"},
		{InstanceKey{AssemblyID: "sys", Version: "1.0.0", Hash: "abc123"}, "sys:1.0.0:abc123:"},
		{InstanceKey{AssemblyID: "sys", Version: "1.0.0", Hash: "abc123", Env: "prod"}, "sys:1.0.0:abc123:prod"},
	}
	for _, tt := range tests {
		got := tt.key.ColonString()
		if got != tt.want {
			t.Errorf("ColonString(%+v) = %q, want %q", tt.key, got, tt.want)
		}

		parsed, err := ParseKey(got)
		if err != nil {
			t.Fatalf("ParseKey(%q): %v", got, err)
		}
		if parsed.Hash != tt.key.Hash || parsed.Env != tt.key.Env {
			t.Errorf("ParseKey(%q) = %+v, want hash %q env %q", got, parsed, tt.key.Hash, tt.key.Env)
		}
	}
}

func TestParseUserKeyAtFormColonRoundTrip(t *testing.T) {
	key, err := ParseUserKey("order-processing-ts@2.0.0")
	if err != nil {
		t.Fatalf("parse at-form: %v", err)
	}
	parsed, err := ParseUserKey(key.ColonString())
	if err != nil {
		t.Fatalf("parse colon form: %v", err)
	}
	if parsed != key {
		t.Errorf("at->colon round trip mismatch: %+v != %+v", parsed, key)
	}
	if parsed.Env != "" || parsed.Hash != "" {
		t.Errorf("expected partial key to stay env/hash-less, got %+v", parsed)
	}
}
