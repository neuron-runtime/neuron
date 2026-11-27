package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/application/connection"
	"github.com/neuron-runtime/neuron/shared/types/apitoken"
	"github.com/neuron-runtime/neuron/shared/types/projectruntime"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// TestR5_24_DuplicateSignalDeliveryCausesOneResume proves that retrying one
// logical delivery cannot perform a second resume. The first delivery must
// complete the waiting execution and write one resume marker; the identical
// retry must be acknowledged without rewriting the marker or changing state.
func TestR5_24_DuplicateSignalDeliveryCausesOneResume(t *testing.T) {
	p := setupSuspendProject(t)
	session := startDurableRuntime(t, p)

	resumeMarker := filepath.Join(t.TempDir(), "r5-24-resume.json")
	executionID := detachSuspendExecution(t, session.record, p.assembly, map[string]any{
		"marker":          filepath.Join(t.TempDir(), "r5-24-started.marker"),
		"resume_marker":   resumeMarker,
		"wait_type":       "signal",
		"expected_signal": "approved",
		"correlation_key": "approval-24",
		"timer_delay_ms":  0,
		"payload_schema":  "",
	})

	waiting := waitForStatusByID(t, session.record, p.assembly, executionID, "waiting", 30*time.Second)
	if len(waiting.Waits) != 1 || waiting.Waits[0].ExpectedSignal != "approved" {
		t.Fatalf("execution %s waits = %#v, want one approved signal wait", executionID, waiting.Waits)
	}

	c := newTestClient(t, session.record)
	delivery := protocol.SignalDeliveryRequest{
		DeliveryID:     "r5-24-delivery-1",
		Payload:        map[string]any{"approved": true, "by": "ada", "resume_marker": resumeMarker},
		CorrelationKey: "approval-24",
	}
	first, err := c.SignalExecution(context.Background(), executionID, "approved", delivery)
	if err != nil {
		t.Fatalf("first SignalExecution: %v\nlog: %s", err, session.log())
	}
	if first.Status != "accepted" && first.Status != "replay" {
		t.Fatalf("first signal response = %#v, want accepted/replay", first)
	}
	if first.DeliveryID != delivery.DeliveryID {
		t.Fatalf("first signal delivery id = %q, want %q", first.DeliveryID, delivery.DeliveryID)
	}

	completed := waitForStatusByID(t, session.record, p.assembly, executionID, "completed", 45*time.Second)
	if completed.Error != "" {
		t.Fatalf("completed execution %s has error: %s\nlog: %s", executionID, completed.Error, session.log())
	}
	if len(completed.Waits) != 0 {
		t.Fatalf("completed execution %s still reports waits: %#v", executionID, completed.Waits)
	}
	if completed.CompletedAt == nil {
		t.Fatalf("completed execution %s has no completion time: %#v", executionID, completed)
	}

	resumed := readResumeMarker(t, resumeMarker, 30*time.Second)
	if resumed["cause"] != "signal" || resumed["signal"] != "approved" || resumed["delivery_id"] != delivery.DeliveryID {
		t.Fatalf("resume marker = %#v, want cause/signal/delivery_id from the first delivery", resumed)
	}
	payload, _ := resumed["payload"].(map[string]any)
	if payload["approved"] != true || payload["by"] != "ada" {
		t.Fatalf("resume payload = %#v, want approved=true by=ada", payload)
	}

	// A resume is the only path that writes the marker, so its content and
	// modification time are an observable write count at the public boundary.
	markerInfo, err := os.Stat(resumeMarker)
	if err != nil {
		t.Fatalf("stat resume marker: %v", err)
	}
	markerContent, err := os.ReadFile(resumeMarker)
	if err != nil {
		t.Fatalf("read resume marker: %v", err)
	}

	second, err := c.SignalExecution(context.Background(), executionID, "approved", delivery)
	if err != nil {
		t.Fatalf("duplicate SignalExecution: %v\nlog: %s", err, session.log())
	}
	if second.Status != "accepted" && second.Status != "replay" {
		t.Fatalf("duplicate signal response = %#v, want accepted/replay", second)
	}

	// A wrongly committed duplicate would re-invoke the capability after the
	// acknowledgement, so observe a bounded settle window before comparing.
	time.Sleep(time.Second)
	afterInfo, err := os.Stat(resumeMarker)
	if err != nil {
		t.Fatalf("stat resume marker after duplicate: %v", err)
	}
	if !afterInfo.ModTime().Equal(markerInfo.ModTime()) {
		t.Fatalf("resume marker was rewritten (mod time %s -> %s): duplicate delivery caused more than one resume", markerInfo.ModTime(), afterInfo.ModTime())
	}
	afterContent, err := os.ReadFile(resumeMarker)
	if err != nil {
		t.Fatalf("read resume marker after duplicate: %v", err)
	}
	if !bytes.Equal(afterContent, markerContent) {
		t.Fatalf("resume marker content changed after duplicate: %s -> %s", markerContent, afterContent)
	}

	final := waitForStatusByID(t, session.record, p.assembly, executionID, "completed", 10*time.Second)
	if final.Error != "" {
		t.Fatalf("execution %s failed after duplicate delivery: %s\nlog: %s", executionID, final.Error, session.log())
	}
	if final.CompletedAt == nil || *final.CompletedAt != *completed.CompletedAt {
		t.Fatalf("completion time changed after duplicate delivery: %#v -> %#v", completed, final)
	}
}

// detachSuspendExecutionWithInstance is the same detach call as the shared
// helper, but also returns the instance ID needed to address the instance-
// scoped signal route.
func detachSuspendExecutionWithInstance(t *testing.T, record projectruntime.Record, target string, params map[string]any) (executionID, instanceID string) {
	t.Helper()
	c := newTestClient(t, record)
	res, err := c.ExecuteByKeyOrTarget(context.Background(), protocol.InstanceKey{}, target, params, "detach")
	if err != nil {
		t.Fatalf("detach execute %s: %v", target, err)
	}
	if res.ExecutionID == "" || res.InstanceID == "" {
		t.Fatalf("detach execute %s returned incomplete ids: %#v", target, res)
	}
	return string(res.ExecutionID), res.InstanceID
}

// postSignal sends one raw signal request so the test can omit or corrupt the
// bearer credential, which the typed client always supplies correctly.
func postSignal(t *testing.T, endpoint, path, token string, body protocol.SignalDeliveryRequest) (int, []byte) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint+path, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set(apitoken.AuthorizationHeader, apitoken.Header(token))
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("read response from %s: %v", path, err)
	}
	return resp.StatusCode, respBody
}

// TestR5_25_SignalDeliveryRejectsUnauthorizedAndWrongExecution proves that the
// signal endpoint refuses a request without a valid token and refuses a delivery
// addressed to an execution owned by another target. Neither rejected path may
// resume a waiting execution; a correctly authorized delivery still resumes
// only its intended target.
func TestR5_25_SignalDeliveryRejectsUnauthorizedAndWrongExecution(t *testing.T) {
	p := setupSuspendProject(t)
	session := startDurableRuntime(t, p)
	if session.record.Token == "" {
		t.Fatalf("project runtime has no API token; authorization cannot be exercised: record=%#v", session.record)
	}

	dir := t.TempDir()
	resumeA := filepath.Join(dir, "resume-A.json")
	resumeB := filepath.Join(dir, "resume-B.json")
	params := func(correlationKey, resumeMarker string) map[string]any {
		return map[string]any{
			"marker":          filepath.Join(dir, correlationKey+"-started.marker"),
			"resume_marker":   resumeMarker,
			"wait_type":       "signal",
			"expected_signal": "approved",
			"correlation_key": correlationKey,
			"timer_delay_ms":  0,
			"payload_schema":  "",
		}
	}

	execA, instA := detachSuspendExecutionWithInstance(t, session.record, p.assembly, params("approval-A", resumeA))
	execB, instB := detachSuspendExecutionWithInstance(t, session.record, p.failing, params("approval-B", resumeB))
	if instA == instB {
		t.Fatalf("two targets resolved to the same instance %q", instA)
	}
	waitForStatusByID(t, session.record, p.assembly, execA, "waiting", 30*time.Second)
	waitForStatusByID(t, session.record, p.failing, execB, "waiting", 30*time.Second)

	endpoint := session.record.Endpoint()
	signalPath := protocol.ExecutionSignal(execA, "approved")
	authBody := protocol.SignalDeliveryRequest{
		DeliveryID:     "r5-25-unauthorized-1",
		Payload:        map[string]any{"approved": true, "resume_marker": resumeA},
		CorrelationKey: "approval-A",
	}
	for _, tc := range []struct {
		name  string
		token string
	}{
		{name: "tokenless", token: ""},
		{name: "incorrect token", token: "not-the-project-token"},
	} {
		code, body := postSignal(t, endpoint, signalPath, tc.token, authBody)
		if code != http.StatusUnauthorized {
			t.Fatalf("%s signal: status = %d, want 401; body: %s", tc.name, code, body)
		}
	}

	// The instance-scoped route must reject an execution owned by the other
	// target even when the request itself is authenticated.
	wrongTargetBody := protocol.SignalDeliveryRequest{
		DeliveryID:     "r5-25-wrong-execution-1",
		Payload:        map[string]any{"approved": true, "resume_marker": resumeB},
		CorrelationKey: "approval-B",
	}
	code, body := postSignal(t, endpoint, protocol.SignalExecution(instA, execB, "approved"), session.record.Token, wrongTargetBody)
	var signalError struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &signalError); err != nil {
		t.Fatalf("decode wrong-execution response: %v\nbody: %s", err, body)
	}
	if code != http.StatusNotFound || signalError.Code != protocol.SignalCodeNotFound {
		t.Fatalf("wrong-execution signal: status = %d code = %q, want 404/%s; body: %s", code, signalError.Code, protocol.SignalCodeNotFound, body)
	}

	// The execution-scoped route must also reject a delivery whose correlation
	// key binds it to the other target's wait.
	wrongCorrelation := protocol.SignalDeliveryRequest{
		DeliveryID:     "r5-25-wrong-correlation-1",
		Payload:        map[string]any{"approved": true, "resume_marker": resumeB},
		CorrelationKey: "approval-B",
	}
	c := newTestClient(t, session.record)
	_, err := c.SignalExecution(context.Background(), execA, "approved", wrongCorrelation)
	if connection.StatusCode(err) != http.StatusNotFound || connection.ResponseCode(err) != protocol.SignalCodeNoMatchingWait {
		t.Fatalf("wrong-correlation signal error = %v (status %d, code %q), want 404/%s", err, connection.StatusCode(err), connection.ResponseCode(err), protocol.SignalCodeNoMatchingWait)
	}

	// None of the rejected deliveries may have resumed either target.
	time.Sleep(500 * time.Millisecond)
	waitForStatusByID(t, session.record, p.assembly, execA, "waiting", 5*time.Second)
	waitForStatusByID(t, session.record, p.failing, execB, "waiting", 5*time.Second)
	for _, path := range []string{resumeA, resumeB} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("resume marker %s exists after rejected deliveries (stat error %v)", path, err)
		}
	}

	// Positive control: an authorized delivery bound to A resumes only A.
	good := protocol.SignalDeliveryRequest{
		DeliveryID:     "r5-25-authorized-1",
		Payload:        map[string]any{"approved": true, "resume_marker": resumeA},
		CorrelationKey: "approval-A",
	}
	resp, err := c.SignalExecution(context.Background(), execA, "approved", good)
	if err != nil {
		t.Fatalf("authorized SignalExecution: %v\nlog: %s", err, session.log())
	}
	if resp.Status != "accepted" && resp.Status != "replay" {
		t.Fatalf("authorized signal response = %#v, want accepted/replay", resp)
	}
	completedA := waitForStatusByID(t, session.record, p.assembly, execA, "completed", 45*time.Second)
	if completedA.Error != "" {
		t.Fatalf("execution %s failed after authorized delivery: %s\nlog: %s", execA, completedA.Error, session.log())
	}
	readResumeMarker(t, resumeA, 30*time.Second)

	waitForStatusByID(t, session.record, p.failing, execB, "waiting", 5*time.Second)
	if _, err := os.Stat(resumeB); !os.IsNotExist(err) {
		t.Fatalf("unintended execution %s resumed (marker %s, stat error %v)", execB, resumeB, err)
	}
}
