package ui

import (
	"bytes"
	"strings"
	"testing"
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

func TestRunnerLineOutput(t *testing.T) {
	// A non-terminal writer (bytes.Buffer) falls back to line-based output
	// with glyphs and no color codes.
	var buf bytes.Buffer
	r := New(&buf)

	r.Step("Resolving example:echo")
	r.StepDone("example:echo@1.0.0 installed")
	r.Info("building executors")
	r.Fail("broken:something failed")

	if r.Live() {
		t.Error("bytes.Buffer must never drive a live spinner")
	}

	r.Stop()

	got := buf.String()
	want := "  · Resolving example:echo ...\n" +
		"  ✓ example:echo@1.0.0 installed\n" +
		"  · building executors\n" +
		"  ✗ broken:something failed\n"

	if got != want {
		t.Fatalf("line output:\n%q\nwant:\n%q", got, want)
	}
	if strings.ContainsAny(got, "\x1b") {
		t.Fatalf("line output must never contain escape sequences: %q", got)
	}
}

func TestRunnerStepDoneClearsSpinnerNothing(t *testing.T) {
	// StepDone/Stop must be safe when no step was started.
	var buf bytes.Buffer
	r := New(&buf)

	r.StepDone("nothing started")
	r.Stop()

	got := buf.String()
	want := "  ✓ nothing started\n"
	if got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
