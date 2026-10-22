package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/shared/types/builtins"
)

func TestHTTPReturnsTheStatusAndTheParsedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"abc"}`))
	}))
	defer server.Close()

	result, err := HTTP{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result["status_code"] != 201 {
		t.Errorf("status_code = %v, want 201", result["status_code"])
	}
	body, ok := result["body"].(map[string]any)
	if !ok {
		t.Fatalf("body = %#v, want a parsed object", result["body"])
	}
	if body["id"] != "abc" {
		t.Errorf("body id = %v, want %q", body["id"], "abc")
	}
}

// A body that is not JSON is still returned, as text. Dropping it would hide
// the server's answer behind an error the author cannot act on.
func TestHTTPKeepsANonJSONBodyAsText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("plain text"))
	}))
	defer server.Close()

	result, err := HTTP{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["body"] != "plain text" {
		t.Errorf("body = %#v, want the raw text", result["body"])
	}
}

// The status is part of the result, not a failure: a 404 is an answer the
// caller asked for.
func TestHTTPReturnsANonSuccessStatusWithoutFailing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	result, err := HTTP{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["status_code"] != 404 {
		t.Errorf("status_code = %v, want 404", result["status_code"])
	}
}

func TestHTTPSendsTheMethodHeadersAndBody(t *testing.T) {
	var received struct {
		method  string
		header  string
		payload string
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		received.method = r.Method
		received.header = r.Header.Get("X-Trace")
		received.payload = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, err := HTTP{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{
			"url":     server.URL,
			"method":  "post",
			"headers": map[string]any{"X-Trace": "abc"},
			"body":    map[string]any{"name": "neuron"},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if received.method != "POST" {
		t.Errorf("method = %q, want POST", received.method)
	}
	if received.header != "abc" {
		t.Errorf("X-Trace = %q, want %q", received.header, "abc")
	}
	if received.payload != `{"name":"neuron"}` {
		t.Errorf("body = %q, want the encoded JSON", received.payload)
	}
}

func TestHTTPRequiresAURL(t *testing.T) {
	_, err := HTTP{}.Execute(context.Background(), contracts.ExecutionContext{})
	if err == nil {
		t.Fatal("Execute succeeded without a url")
	}
}

func TestHTTPReportsAnUnreachableServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := server.URL
	server.Close()

	_, err := HTTP{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": url},
	})
	if err == nil {
		t.Fatal("Execute succeeded for a server that is not listening")
	}
}

// A response body that exceeds the limit is refused rather than truncated: a
// caller that receives a shortened body cannot tell it apart from a complete
// one.
func TestHTTPRefusesAResponseBodyAboveTheLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("0123456789"))
	}))
	defer server.Close()

	_, err := HTTP{Policy: builtins.Policy{MaxHTTPResponseBytes: 4}}.Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL},
	})
	if err == nil {
		t.Fatal("Execute accepted a response body above the limit")
	}
	if !strings.Contains(err.Error(), "exceeds the limit") {
		t.Errorf("error = %v, want it to state the limit", err)
	}
}

func TestHTTPAcceptsAResponseBodyAtTheLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("0123456789"))
	}))
	defer server.Close()

	result, err := HTTP{Policy: builtins.Policy{MaxHTTPResponseBytes: 10}}.Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["body"] != "0123456789" {
		t.Errorf("body = %#v, want the whole body", result["body"])
	}
}

func TestHTTPRefusesARequestBodyAboveTheLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	_, err := HTTP{Policy: builtins.Policy{MaxHTTPRequestBodyBytes: 4}}.Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{
			"url":  server.URL,
			"body": map[string]any{"name": "neuron"},
		},
	})
	if err == nil {
		t.Fatal("Execute accepted a request body above the limit")
	}
	if !strings.Contains(err.Error(), "exceeds the limit") {
		t.Errorf("error = %v, want it to state the limit", err)
	}
}
