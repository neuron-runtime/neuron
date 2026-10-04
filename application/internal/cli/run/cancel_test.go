package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/application/client"
	"github.com/neuron-runtime/neuron/application/connection"
	"github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
	"github.com/spf13/cobra"
)

// cancelConnection is a Connection that records the cancellation request and
// answers with a canned response, so the CLI's Ctrl-C path can be exercised
// without a running N.O.R.E.
type cancelConnection struct {
	mu           sync.Mutex
	calls        []string
	bodies       []string
	status       int
	response     string
	err          error
	called       chan struct{}
	once         sync.Once
	ctxErrAtCall error
}

func (c *cancelConnection) Do(ctx context.Context, method, path string, body any, out any) error {
	payload, _ := json.Marshal(body)

	c.mu.Lock()
	c.calls = append(c.calls, method+" "+path)
	c.bodies = append(c.bodies, string(payload))
	c.ctxErrAtCall = ctx.Err()
	status, response, failure := c.status, c.response, c.err
	c.mu.Unlock()

	c.once.Do(func() { close(c.called) })

	if failure != nil {
		return failure
	}
	if status != 0 && (status < 200 || status >= 300) {
		return &connection.StatusError{Code: status, Message: response}
	}
	if out != nil && response != "" {
		return json.Unmarshal([]byte(response), out)
	}
	return nil
}

func (c *cancelConnection) Stream(context.Context, string, string, any, func([]byte) error) error {
	return nil
}

func (c *cancelConnection) OpenWebSocket(context.Context, string) (*connection.WebSocketStream, error) {
	return nil, connection.ErrWebSocketUnavailable
}

func (c *cancelConnection) Health(context.Context) error { return nil }
func (c *cancelConnection) Close() error                 { return nil }

// errAtCall reports the request context's state when the request was issued.
func (c *cancelConnection) errAtCall() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ctxErrAtCall
}

func (c *cancelConnection) lastCall() (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) == 0 {
		return "", ""
	}
	return c.calls[len(c.calls)-1], c.bodies[len(c.calls)-1]
}

// interruptSelf sends this process a real interrupt, which is what an operator
// pressing Ctrl-C actually delivers. Sending a real signal rather than calling the
// handler is the point: it proves the CLI reacts to the signal, not just to a
// function call shaped like one.
func interruptSelf(t *testing.T) {
	t.Helper()
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Skipf("cannot find this process: %v", err)
	}
	if err := process.Signal(os.Interrupt); err != nil {
		t.Skipf("cannot deliver an interrupt to this process: %v", err)
	}
}

// syncBuffer collects the CLI's diagnostics. The signal-handling goroutine writes
// them while the test reads them, so the buffer is guarded rather than a plain
// bytes.Buffer, which the race detector would rightly reject.
type syncBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// newCancelCommand builds the minimal cobra command shape the handler writes to.
func newCancelCommand() (*cobra.Command, *syncBuffer) {
	stderr := &syncBuffer{}
	cmd := &cobra.Command{}
	cmd.SetErr(stderr)
	return cmd, stderr
}

// waitForCall blocks until the cancellation request has been issued.
func waitForCall(t *testing.T, called <-chan struct{}) {
	t.Helper()
	select {
	case <-called:
	case <-time.After(10 * time.Second):
		t.Fatal("Ctrl-C never issued a cancellation request")
	}
}

// Ctrl-C has to reach N.O.R.E. rather than just killing the CLI. The execution runs
// in the runtime, so a process that exits without asking leaves the work running
// with nobody watching it.
func TestCtrlCRequestsCancellationOfTheRunningExecution(t *testing.T) {
	conn := &cancelConnection{
		status:   http.StatusOK,
		response: `{"data":{"id":"exec_1","status":"cancelled"}}`,
		called:   make(chan struct{}),
	}
	cmd, stderr := newCancelCommand()

	stop := cancelOnInterrupt(context.Background(), cmd, client.New(conn), "inst_1", core.ID("exec_1"))
	defer stop()

	interruptSelf(t)
	waitForCall(t, conn.called)

	method, _ := conn.lastCall()
	if !strings.HasPrefix(method, http.MethodPost+" /v1/instances/inst_1/executions/exec_1/cancel") {
		t.Errorf("request = %q, want a POST to the cancel endpoint", method)
	}

	waitForMessage(t, stderr, "cancelled")
}

// The reason travels with the request so the execution history says why it stopped,
// rather than leaving an operator to guess.
func TestCtrlCSendsTheReason(t *testing.T) {
	conn := &cancelConnection{
		status:   http.StatusOK,
		response: `{"data":{"id":"exec_1","status":"cancelled"}}`,
		called:   make(chan struct{}),
	}
	cmd, _ := newCancelCommand()

	stop := cancelOnInterrupt(context.Background(), cmd, client.New(conn), "inst_1", core.ID("exec_1"))
	defer stop()

	interruptSelf(t)
	waitForCall(t, conn.called)

	_, body := conn.lastCall()
	if !strings.Contains(body, "reason") || !strings.Contains(body, "CLI") {
		t.Errorf("request body = %s, want it to state a reason", body)
	}
}

// The request that implements the interrupt cannot ride on the interrupted context,
// or it would be cancelled before it is sent and never reach N.O.R.E.
//
// The parent context is cancelled *before* the interrupt so the assertion is
// deterministic: if the request inherited the cancelled context, it would be dead
// on arrival and this would fail rather than pass by luck.
func TestCtrlCCancelRequestOutlivesTheInterrupt(t *testing.T) {
	conn := &cancelConnection{
		status:   http.StatusOK,
		response: `{"data":{"id":"exec_1","status":"cancelled"}}`,
		called:   make(chan struct{}),
	}
	cmd, _ := newCancelCommand()

	ctx, cancel := context.WithCancel(context.Background())
	stop := cancelOnInterrupt(ctx, cmd, client.New(conn), "inst_1", core.ID("exec_1"))
	defer stop()

	cancel()
	if ctx.Err() == nil {
		t.Fatal("parent context is live, want it cancelled before the interrupt")
	}

	interruptSelf(t)
	waitForCall(t, conn.called)

	if err := conn.errAtCall(); err != nil {
		t.Errorf("request context was %v when sent, want it live so the request can be delivered", err)
	}
}

// An execution that finished on its own must not be reported as stopped. Telling an
// operator their cancel worked when it did nothing is worse than saying nothing.
func TestCtrlCReportsAnAlreadyFinishedExecution(t *testing.T) {
	conn := &cancelConnection{
		status:   http.StatusConflict,
		response: "already reached a terminal state",
		called:   make(chan struct{}),
	}
	cmd, stderr := newCancelCommand()

	stop := cancelOnInterrupt(context.Background(), cmd, client.New(conn), "inst_1", core.ID("exec_1"))
	defer stop()

	interruptSelf(t)
	waitForCall(t, conn.called)

	waitForMessage(t, stderr, "already finished")
}

// The three outcomes must be distinguishable. Collapsing them would leave an
// operator unsure whether anything is still running.
func TestReportCancellationDistinguishesEveryOutcome(t *testing.T) {
	tests := []struct {
		name   string
		item   protocol.ExecutionItem
		err    error
		wantIn string
	}{
		{
			name:   "stopped by this request",
			item:   protocol.ExecutionItem{ID: "exec_1", Status: "cancelled"},
			wantIn: "cancelled",
		},
		{
			name:   "already finished",
			err:    &connection.StatusError{Code: http.StatusConflict, Message: "terminal"},
			wantIn: "already finished",
		},
		{
			name:   "no such execution",
			err:    &connection.StatusError{Code: http.StatusNotFound, Message: "not found"},
			wantIn: "not found",
		},
		{
			name:   "runtime unreachable",
			err:    errors.New("connection refused"),
			wantIn: "could not cancel",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd, stderr := newCancelCommand()
			reportCancellation(cmd, core.ID("exec_1"), test.item, test.err)
			if !strings.Contains(stderr.String(), test.wantIn) {
				t.Errorf("message = %q, want it to contain %q", stderr.String(), test.wantIn)
			}
		})
	}
}

// waitForMessage blocks until the reported output contains want. The write happens
// on the signal-handling goroutine, so the assertion has to re-read the buffer
// rather than inspect a snapshot taken before the message could exist.
func waitForMessage(t *testing.T, output *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(output.String(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("output = %q, want it to contain %q", output.String(), want)
}
