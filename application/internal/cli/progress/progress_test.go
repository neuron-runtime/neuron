package progress

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Muhammad-Jay/neuron/application/executor"
)

func TestSGRStripper(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "drops foreground color around glyph",
			input: "\x1b[37m⠋\x1b[0m Resolving example:echo",
			want:  "⠋ Resolving example:echo",
		},
		{
			name:  "keeps erase and carriage return",
			input: "\r\x1b[K\r Resolving",
			want:  "\r\x1b[K\r Resolving",
		},
		{
			name:  "keeps cursor control",
			input: "\x1b[?25l",
			want:  "\x1b[?25l",
		},
		{
			name:  "keeps non-SGR CSI",
			input: "\x1b[2K",
			want:  "\x1b[2K",
		},
		{
			name:  "strips multi-parameter SGR",
			input: "x\x1b[1;31my",
			want:  "xy",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			stripper := &sgrStripper{w: &buf}

			// Split half way through to exercise cross-write state.
			mid := len(tc.input) / 2
			if _, err := stripper.Write([]byte(tc.input[:mid])); err != nil {
				t.Fatal(err)
			}
			if _, err := stripper.Write([]byte(tc.input[mid:])); err != nil {
				t.Fatal(err)
			}

			if got := buf.String(); got != tc.want {
				t.Fatalf("stripped output = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReporterLineOutput(t *testing.T) {
	// A non-terminal writer (bytes.Buffer) falls back to line-based output
	// with check marks and no color codes.
	var buf bytes.Buffer
	rep := New(&buf)

	rep.Resolving(executor.Requirement{Type: "example:echo", Version: "^1.0.0"})
	rep.Installing(executor.Package{Type: "example:echo", Version: "1.0.0", Registry: "local"})
	rep.Installed(executor.InstallResult{
		Installed: &executor.Installed{Type: "example:echo", Version: "1.0.0"},
	})

	rep.Stop()

	got := buf.String()
	want := "  · Resolving example:echo@^1.0.0 ...\n" +
		"  · Installing example:echo@1.0.0 (from local) ...\n" +
		"  ✓ example:echo@1.0.0 installed\n"

	if got != want {
		t.Fatalf("line output:\n%q\nwant:\n%q", got, want)
	}
	if strings.ContainsAny(got, "\x1b") {
		t.Fatalf("line output must never contain escape sequences: %q", got)
	}
}

func TestReporterAlreadyInstalled(t *testing.T) {
	var buf bytes.Buffer
	rep := New(&buf)

	rep.Resolving(executor.Requirement{Type: "example:echo"})
	rep.AlreadyInstalled(
		executor.Requirement{Type: "example:echo"},
		executor.Installed{Type: "example:echo", Version: "1.0.0"},
	)
	rep.Stop()

	got := buf.String()
	want := "  · Resolving example:echo ...\n" +
		"  ✓ example:echo@1.0.0 already installed\n"

	if got != want {
		t.Fatalf("line output:\n%q\nwant:\n%q", got, want)
	}
}
