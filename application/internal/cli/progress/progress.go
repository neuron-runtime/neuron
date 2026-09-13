// Package progress renders executor resolution and installation progress for
// the CLI.
//
// It implements executor.Observer so the resolution pipeline stays output-free:
// the executor package reports events, and this layer decides how to draw
// them. On a terminal it drives a live spinner; on a pipe it falls back to
// plain, line-oriented progress so logs remain readable. Output never carries
// color codes.
package progress

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/Muhammad-Jay/neuron/application/executor"
	"github.com/briandowns/spinner"
	"github.com/mattn/go-isatty"
)

// glyphs used for the line-oriented (non-TTY) fallback and completion lines.
const (
	// bullet marks an in-flight step.
	bullet = "·"

	// check marks a completed step.
	check = "✓"
)

// Reporter renders executor.Observer events for one command invocation.
//
// A Reporter is not safe for concurrent use beyond the internal spinner
// lifecycle; resolution events are emitted sequentially by the pipeline.
type Reporter struct {
	out     io.Writer
	spin    *spinner.Spinner
	enabled bool
	mu      sync.Mutex
}

// New returns a Reporter writing to out (stderr for CLI commands). Spinner
// rendering is enabled only when out is a real terminal; piped output uses
// the line-based fallback.
func New(out io.Writer) *Reporter {
	if out == nil {
		out = io.Discard
	}

	r := &Reporter{out: out}

	if f, ok := out.(*os.File); ok {
		if isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd()) {
			r.enabled = true
			r.spin = spinner.New(
				spinner.CharSets[14],
				100*time.Millisecond,
				spinner.WithWriter(&sgrStripper{w: out}),
				spinner.WithHiddenCursor(true),
			)
		}
	}

	return r
}

// Resolving implements executor.Observer.
func (r *Reporter) Resolving(res executor.Requirement) {
	r.begin("Resolving " + display(res.Type, res.Version))
}

// AlreadyInstalled implements executor.Observer.
func (r *Reporter) AlreadyInstalled(_ executor.Requirement, installed executor.Installed) {
	r.complete(display(installed.Type, installed.Version) + " already installed")
}

// Installing implements executor.Observer.
func (r *Reporter) Installing(pkg executor.Package) {
	text := "Installing " + display(pkg.Type, pkg.Version)
	if pkg.Registry != "" {
		text += " (from " + pkg.Registry + ")"
	}
	r.begin(text)
}

// Installed implements executor.Observer.
func (r *Reporter) Installed(result executor.InstallResult) {
	if result.Installed == nil {
		return
	}
	verb := " installed"
	if result.AlreadyPresent {
		verb = " already installed"
	}
	r.complete(display(result.Installed.Type, result.Installed.Version) + verb)
}

// Stop halts any active spinner and restores the cursor. It is safe to call
// when nothing is running (e.g. from a deferred cleanup).
func (r *Reporter) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.spin != nil && r.spin.Active() {
		r.spin.Stop()
	}
}

// begin announces an in-flight step: the spinner suffix on a terminal, or a
// plain line on a pipe.
func (r *Reporter) begin(action string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.enabled {
		r.spin.Suffix = " " + action
		r.spin.Start()
		return
	}

	fmt.Fprintf(r.out, "  %s %s ...\n", bullet, action)
}

// complete terminates the active step with a check-marked line.
func (r *Reporter) complete(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.spin != nil && r.spin.Active() {
		r.spin.Stop()
	}

	fmt.Fprintf(r.out, "  %s %s\n", check, text)
}

// display renders a name@version pair, omitting the version when empty.
func display(name, version string) string {
	if version == "" {
		return name
	}
	return name + "@" + version
}

// sgrStripper drops ANSI SGR color sequences (\x1b[<params>m) from a stream
// while passing every other escape sequence through. The spinner's cursor
// control (\x1b[?25l/h) and line-erase codes (\r, \x1b[K) are preserved, so
// the only thing removed is color.
type sgrStripper struct {
	w io.Writer

	// esc accumulates the escape sequence currently being parsed.
	esc []byte

	// state: 0 idle, 1 saw ESC, 2 inside a CSI (ESC [ ...) sequence.
	state int
}

func (s *sgrStripper) Write(p []byte) (int, error) {
	for _, b := range p {
		switch s.state {
		case 0:
			if b == 0x1b {
				s.esc = append(s.esc, b)
				s.state = 1
				continue
			}
			if _, err := s.w.Write([]byte{b}); err != nil {
				return 0, err
			}
		case 1:
			s.esc = append(s.esc, b)
			if b == '[' {
				s.state = 2
				continue
			}
			// A non-CSI escape is complete after ESC + final byte.
			if _, err := s.w.Write(s.esc); err != nil {
				return 0, err
			}
			s.reset()
		case 2:
			if b >= 0x40 && b <= 0x7e {
				s.esc = append(s.esc, b)
				// SGR (m) is color and is dropped; every other CSI
				// sequence is forwarded untouched.
				if b != 'm' {
					if _, err := s.w.Write(s.esc); err != nil {
						return 0, err
					}
				}
				s.reset()
				continue
			}
			// Parameter/intermediate byte (0x20-0x3f); keep collecting.
			s.esc = append(s.esc, b)
		}
	}
	return len(p), nil
}

func (s *sgrStripper) reset() {
	s.esc = s.esc[:0]
	s.state = 0
}
