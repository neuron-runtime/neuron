package ref

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantName    string
		wantVersion string
		wantOK      bool
	}{
		{name: "name only", in: "github:read", wantName: "github:read", wantOK: true},
		{name: "name and version", in: "github:read@1.2.0", wantName: "github:read", wantVersion: "1.2.0", wantOK: true},
		{name: "constraint", in: "github:read@^1.0.0", wantName: "github:read", wantVersion: "^1.0.0", wantOK: true},
		{name: "trims whitespace", in: "  github:read @ 1.2.0 ", wantName: "github:read", wantVersion: "1.2.0", wantOK: true},
		{name: "empty", in: "", wantOK: false},
		{name: "whitespace only", in: "   ", wantOK: false},
		{name: "empty name with version", in: "@1.2.0", wantVersion: "1.2.0", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotVersion, gotOK := Parse(tt.in)
			if gotName != tt.wantName || gotVersion != tt.wantVersion || gotOK != tt.wantOK {
				t.Fatalf("Parse(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.in, gotName, gotVersion, gotOK, tt.wantName, tt.wantVersion, tt.wantOK)
			}
		})
	}
}
