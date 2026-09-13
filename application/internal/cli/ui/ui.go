// Package ui renders CLI status output: step progress, terminal status lines,
// and aggregate concurrent-work displays. It is the only layer that knows
// about spinners, cursor control, and ANSI SGR color sequences.
//
// A Runner writes everything it is given; commands pass stderr for progress so
// stdout stays reserved for machine-readable results. On a terminal it drives
// a live spinner with a hidden cursor; on a pipe it falls back to plain,
// line-oriented output using · / ✓ / ✗ glyphs. Line output never carries
// color codes, so logs and test fixtures stay readable.
package ui

import (
	"io"
	"os"
	"sync"
	"time"

	"github.com/briandowns/spinner"
	"github.com/mattn/go-isatty"
)

// glyphs used for the line-oriented (non-TTY) fallback and completion lines.
const (
	// bullet marks an in-flight or informational line.
	bullet = "\u00b7"

	// check marks a completed step.
	check = "\u2713"

	// cross marks a failed step.
	cross = "\u2717"
)

// Runner renders step progress for one command invocation against out.
//
// A Runner is not safe for concurrent use beyond the internal spinner
// lifecycle; status events are emitted sequentially by the pipeline. The
// mutex exists so a lingering spinner stop cannot interleave with a completed
// line from another goroutine.
type Runner struct {
	out     io.Writer
	spin    *spinner.Spinner
	enabled bool
	mu      sync.Mutex
}

// New returns a Runner writing to out (stderr for CLI commands). Spinner
// rendering is enabled only when out is a real terminal; piped output uses the
// glyph-based line fallback.
func New(out io.Writer) *Runner {
	if out == nil {
		out = io.Discard
	}

	r := &Runner{out: out}

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

// Step announces an in-flight step: the spinner suffix on a terminal, or a
// bullet line on a pipe.
func (r *Runner) Step(label string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.spin != nil {
		r.spin.Suffix = " " + label
		r.spin.Start()
		return
	}

	line(r.out, bullet, label+" ...")
}

// StepDone terminates the active step with a check-marked line.
func (r *Runner) StepDone(label string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.spin != nil && r.spin.Active() {
		r.spin.Stop()
	}

	line(r.out, check, label)
}

// StepFail terminates the active step with a cross-marked line.
func (r *Runner) StepFail(label string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.spin != nil && r.spin.Active() {
		r.spin.Stop()
	}

	line(r.out, cross, label)
}

// OK renders a check-marked status line (an interstitial completion, not a
// step that a Step() immediately preceded).
func (r *Runner) OK(label string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.spin != nil && r.spin.Active() {
		r.spin.Stop()
	}

	line(r.out, check, label)
}

// Fail renders a cross-marked status line.
func (r *Runner) Fail(label string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.spin != nil && r.spin.Active() {
		r.spin.Stop()
	}

	line(r.out, cross, label)
}

// Info renders an informational bullet line.
func (r *Runner) Info(label string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.spin != nil && r.spin.Active() {
		r.spin.Stop()
	}

	r.out.Write([]byte("  " + bullet + " " + label + "\n"))
}

// Busy drives the aggregate concurrent-work display: on a terminal the
// spinner shows count, while on a pipe each counted item is emitted as its own
// Step line (the terminal lines that follow carry the outcome).
func (r *Runner) Busy(count int, label string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.enabled {
		if count > 1 {
			r.spin.Suffix = " " + label + " (" + itoa(count) + ")"
		} else {
			r.spin.Suffix = " " + label
		}
		r.spin.Start()
	}
}

// Live reports whether the runner drives a live spinner (a real terminal),
// letting callers decide between aggregate and line-wise presentations.
func (r *Runner) Live() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.enabled
}

// Stop halts any active spinner and restores the cursor. It is safe to call
// when nothing is running (e.g. from a deferred cleanup).
func (r *Runner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.spin != nil && r.spin.Active() {
		r.spin.Stop()
	}
}

// line writes a single progress line: two-space indent, glyph, label.
func line(w io.Writer, glyph, text string) {
	w.Write([]byte("  " + glyph + " " + text + "\n"))
}

func itoa(n int) string {
	if n < 0 {
		return "-1"
	}
	var buf [20]byte
	i := len(buf)
	for n > 9 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	i--
	buf[i] = byte('0' + n)
	return string(buf[i:])
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
