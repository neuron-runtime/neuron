package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/shared/types/builtins"
)

// localHTTP returns a runtime that may reach the loopback interface, which
// is where the test servers live. The runtime refuses such an address by
// default, so a test that wants to talk to a local server has to say so.
func localHTTP() HTTP {
	return HTTP{Policy: builtins.Policy{AllowedHTTPHosts: []string{"127.0.0.1"}}}
}

func TestHTTPReturnsTheStatusAndTheParsedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"abc"}`))
	}))
	defer server.Close()

	result, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
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

// The response headers are part of the answer: a caller that cannot see them
// cannot read a rate-limit header or a content type.
func TestHTTPReturnsTheResponseHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Trace", "abc")
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	result, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	headers, ok := result["headers"].(map[string]string)
	if !ok {
		t.Fatalf("headers = %#v, want a map", result["headers"])
	}
	if headers["X-Trace"] != "abc" {
		t.Errorf("X-Trace = %q, want %q", headers["X-Trace"], "abc")
	}
}

// A body that is not JSON is refused in the JSON format: a caller that asked
// for a structured answer cannot be handed a string and told it is a value.
func TestHTTPRefusesANonJSONBodyInTheJSONFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("plain text"))
	}))
	defer server.Close()

	_, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL},
	})
	if err == nil {
		t.Fatal("Execute accepted a body that is not JSON")
	}
	if !strings.Contains(err.Error(), "not JSON") {
		t.Errorf("error = %v, want it to state that the body is not JSON", err)
	}
}

// The text format is how a caller asks for a body that is not JSON.
func TestHTTPKeepsANonJSONBodyAsText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("plain text"))
	}))
	defer server.Close()

	result, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL, "format": "text"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["body"] != "plain text" {
		t.Errorf("body = %#v, want the raw text", result["body"])
	}
}

// An empty body is an answer, not a malformed one.
func TestHTTPAcceptsAnEmptyBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	result, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["body"] != nil {
		t.Errorf("body = %#v, want no body", result["body"])
	}
}

// The status is part of the result, not a failure: a 404 is an answer the
// caller asked for.
func TestHTTPReturnsANonSuccessStatusWithoutFailing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	result, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
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

	_, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
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

// A url names a GET. A caller that wants another method asks for it.
func TestHTTPDefaultsToGET(t *testing.T) {
	var method string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	_, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if method != http.MethodGet {
		t.Errorf("method = %q, want GET", method)
	}
}

func TestHTTPRequiresAURL(t *testing.T) {
	_, err := HTTP{}.Execute(context.Background(), contracts.ExecutionContext{})
	if err == nil {
		t.Fatal("Execute succeeded without a url")
	}
}

func TestHTTPRejectsAnUnsupportedScheme(t *testing.T) {
	_, err := HTTP{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": "ftp://example.com/file"},
	})
	if err == nil {
		t.Fatal("Execute accepted an unsupported url scheme")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error = %v, want it to state that the scheme is not supported", err)
	}
}

func TestHTTPReportsAnUnreachableServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := server.URL
	server.Close()

	_, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
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

	_, err := HTTP{Policy: builtins.Policy{
		AllowedHTTPHosts:     []string{"127.0.0.1"},
		MaxHTTPResponseBytes: 4,
	}}.Execute(context.Background(), contracts.ExecutionContext{
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

	result, err := HTTP{Policy: builtins.Policy{
		AllowedHTTPHosts:     []string{"127.0.0.1"},
		MaxHTTPResponseBytes: 10,
	}}.Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL, "format": "text"},
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

	_, err := HTTP{Policy: builtins.Policy{
		AllowedHTTPHosts:        []string{"127.0.0.1"},
		MaxHTTPRequestBodyBytes: 4,
	}}.Execute(context.Background(), contracts.ExecutionContext{
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

// The query parameters are appended to the URL, so a caller can shape the
// request without building the URL itself.
func TestHTTPAppendsTheQueryParameters(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	_, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{
			"url":   server.URL,
			"query": map[string]any{"page": "2"},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if query != "page=2" {
		t.Errorf("query = %q, want %q", query, "page=2")
	}
}

// The bearer credentials are sent as an authorization header, and never as
// part of the URL.
func TestHTTPSendsBearerCredentials(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	_, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{
			"url":  server.URL,
			"auth": map[string]any{"type": "bearer", "token": "abc"},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if authorization != "Bearer abc" {
		t.Errorf("Authorization = %q, want %q", authorization, "Bearer abc")
	}
}

// The basic credentials are sent as an authorization header.
func TestHTTPSendsBasicCredentials(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	_, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{
			"url":  server.URL,
			"auth": map[string]any{"type": "basic", "username": "neuron", "password": "secret"},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if authorization != "Basic bmV1cm9uOnNlY3JldA==" {
		t.Errorf("Authorization = %q, want the basic credentials", authorization)
	}
}

// An API key is sent in the header it names.
func TestHTTPSendsAnAPIKey(t *testing.T) {
	var key string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key = r.Header.Get("X-Api-Key")
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	_, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{
			"url":  server.URL,
			"auth": map[string]any{"type": "apiKey", "name": "X-Api-Key", "value": "abc"},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if key != "abc" {
		t.Errorf("X-Api-Key = %q, want %q", key, "abc")
	}
}

// An authentication type that does not exist is refused rather than ignored.
func TestHTTPRefusesAnUnknownAuthenticationType(t *testing.T) {
	_, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{
			"url":  "https://example.com",
			"auth": map[string]any{"type": "magic"},
		},
	})
	if err == nil {
		t.Fatal("Execute accepted an unknown authentication type")
	}
	if !strings.Contains(err.Error(), "unknown authentication type") {
		t.Errorf("error = %v, want it to state that the type is unknown", err)
	}
}

// The request carries the content type the caller asked for.
func TestHTTPSendsTheContentType(t *testing.T) {
	var contentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	_, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{
			"url":         server.URL,
			"method":      "POST",
			"contentType": "application/xml",
			"body":        map[string]any{"name": "neuron"},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if contentType != "application/xml" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/xml")
	}
}

// The answer carries the content type of the response, so a caller can tell
// what it received.
func TestHTTPReportsTheResponseContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	result, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["content_type"] != "application/json" {
		t.Errorf("content_type = %v, want %q", result["content_type"], "application/json")
	}
}

// The answer says how long the request took.
func TestHTTPReportsTheDuration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	result, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, ok := result["duration_ms"].(float64); !ok {
		t.Errorf("duration_ms = %#v, want a number", result["duration_ms"])
	}
}

// The retry policy names the methods that may be retried.
func TestHTTPRetriesOnlyTheNamedMethods(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{
			"url":    server.URL,
			"method": "POST",
			"retry":  map[string]any{"attempts": 3, "backoff": "10ms", "methods": []any{"POST"}},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("the server was asked %d times, want 3", got)
	}
}
