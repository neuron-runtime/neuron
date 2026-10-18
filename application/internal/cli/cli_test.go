package cli

import (
	"testing"
)

// TestCommandTreeResolvesDocumentedSurfaces pins the cobra command tree to the
// CLI surface the documentation promises. A Use string containing a space
// ("capability runtime") silently collapses into a single command named after
// its first word, leaving the documented child forms unreachable — this test
// fails if any documented invocation stops resolving to its leaf command.
func TestCommandTreeResolvesDocumentedSurfaces(t *testing.T) {
	cases := []struct {
		name string
		args []string
		leaf string
		rest int
	}{
		{"capability runtime list", []string{"capability", "runtime", "list"}, "list", 0},
		{"capability runtime inspect", []string{"capability", "runtime", "inspect", "example:echo"}, "inspect [name@version]", 1},
		{"capability runtime group", []string{"capability", "runtime"}, "runtime", 0},
		{"instance list", []string{"instance", "list"}, "list [instance-id]", 0},
		{"instance remove", []string{"instance", "remove", "key"}, "remove [instance-id|assembly-key]", 1},
		{"instance clear", []string{"instance", "clear"}, "clear", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, rest, err := RootCmd.Find(tc.args)
			if err != nil {
				t.Fatalf("Find(%v): %v", tc.args, err)
			}
			if cmd.Use != tc.leaf {
				t.Errorf("resolved command Use = %q, want %q", cmd.Use, tc.leaf)
			}
			if len(rest) != tc.rest {
				t.Errorf("unresolved args = %v, want %d remaining", rest, tc.rest)
			}
		})
	}
}

// TestUndocumentedCapabilityListIsNotACommand keeps the accidental
// `neuron capability list` form (a side effect of the old collapsed Use
// string) from reappearing: only `capability runtime list` is the surface.
func TestUndocumentedCapabilityListIsNotACommand(t *testing.T) {
	cmd, rest, err := RootCmd.Find([]string{"capability", "list"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(rest) == 0 {
		t.Errorf("capability list resolved to %q with no remaining args; the list command must live under capability runtime", cmd.Use)
	}
}
