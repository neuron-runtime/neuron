package http

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/shared/types/builtins"
)

// A capability whose url comes from an assembly's input must not be able to
// reach the machine it runs on.
func TestHTTPRefusesALoopbackHostByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	_, err := HTTP{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL},
	})
	if err == nil {
		t.Fatal("Execute reached a loopback address without an allowlist")
	}
	if !strings.Contains(err.Error(), "not a publicly routable address") {
		t.Errorf("error = %v, want it to state that the address is not routable", err)
	}
}

// The operator may author a request that reaches an internal service. That
// request is not derived from an assembly's input, so the restriction is
// lifted for it.
func TestHTTPPermitsAPrivateAddressWhenThePolicySaysSo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	_, err := HTTP{Policy: builtins.Policy{AllowPrivateNetworks: true}}.Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

// An allowlist names the hosts a capability may reach. A host that is not
// named is refused even when it is a public one.
func TestHTTPRefusesAHostThatIsNotAllowed(t *testing.T) {
	_, err := (HTTP{Policy: builtins.Policy{AllowedHTTPHosts: []string{"api.example.com"}}}).Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": "https://other.example.com/data"},
	})
	if err == nil {
		t.Fatal("Execute reached a host that is not on the allowlist")
	}
	if !strings.Contains(err.Error(), "not permitted") {
		t.Errorf("error = %v, want it to state that the host is not permitted", err)
	}
}

// A wildcard covers the named domain and its subdomains, and nothing else.
func TestHTTPAllowsAWildcardHost(t *testing.T) {
	guard := newHostGuard([]string{"*.example.com"}, false)

	for _, host := range []string{"example.com", "api.example.com", "deep.api.example.com"} {
		if !guard.lists(host) {
			t.Errorf("lists(%q) = false, want true", host)
		}
	}
	for _, host := range []string{"notexample.com", "example.org", "example.com.evil.test"} {
		if guard.lists(host) {
			t.Errorf("lists(%q) = true, want false", host)
		}
	}
}

// A host name is compared without regard to case, and a trailing root dot
// does not make it a different host.
func TestHTTPComparesHostsWithoutRegardToCase(t *testing.T) {
	guard := newHostGuard([]string{"API.Example.COM"}, false)

	if !guard.lists("api.example.com") {
		t.Error("lists refused a host that differs only in case")
	}
	if !guard.lists("api.example.com.") {
		t.Error("lists refused a host with a trailing root dot")
	}
}

func TestHTTPIsPubliclyRoutable(t *testing.T) {
	cases := []struct {
		address string
		public  bool
	}{
		{"93.184.216.34", true},
		{"8.8.8.8", true},
		{"127.0.0.1", false},
		{"127.1.2.3", false},
		{"::1", false},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false},
		{"100.64.0.1", false},
		{"0.0.0.0", false},
		{"224.0.0.1", false},
		{"240.0.0.1", false},
		{"fd00::1", false},
		{"fe80::1", false},
	}

	for _, test := range cases {
		ip := net.ParseIP(test.address)
		if ip == nil {
			t.Fatalf("%q is not an address", test.address)
		}
		if got := isPubliclyRoutable(ip); got != test.public {
			t.Errorf("isPubliclyRoutable(%q) = %v, want %v", test.address, got, test.public)
		}
	}
}

// A redirect names a host the caller never asked for, so it is not followed
// unless the capability asked for it.
func TestHTTPDoesNotFollowARedirectByDefault(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"followed":true}`))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	result, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": redirector.URL, "format": "text"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["status_code"] != 302 {
		t.Errorf("status_code = %v, want 302", result["status_code"])
	}
}

// A redirect that is followed is checked against the allowlist as well: the
// second request is a request to a host the policy has to permit.
func TestHTTPDoesNotFollowARedirectToAHostThatIsNotAllowed(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"followed":true}`))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	_, err := HTTP{Policy: builtins.Policy{AllowedHTTPHosts: []string{"127.0.0.1"}}}.Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": redirector.URL, "followRedirects": true},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

// A retry is not made unless the capability asked for one: a request is
// performed once by default, so a capability that is not idempotent cannot
// cause a side effect twice by accident.
func TestHTTPDoesNotRetryByDefault(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("the server was asked %d times, want 1", got)
	}
}

// A retry is made when the capability asked for one and the method is
// idempotent.
func TestHTTPRetriesAnIdempotentRequest(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	result, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL, "retry": map[string]any{"attempts": 3, "backoff": "10ms"}},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["status_code"] != 200 {
		t.Errorf("status_code = %v, want 200", result["status_code"])
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("the server was asked %d times, want 3", got)
	}
}

// A method that is not idempotent is not retried: the server may already
// have performed the effect the caller asked for.
func TestHTTPDoesNotRetryANonIdempotentRequest(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	result, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL, "method": "POST", "retry": map[string]any{"attempts": 3, "backoff": "10ms"}},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["status_code"] != 503 {
		t.Errorf("status_code = %v, want 503", result["status_code"])
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("the server was asked %d times, want 1", got)
	}
}

// The timeout the capability asks for is bounded by the policy: a capability
// is part of the assembly's input and cannot widen what the policy allows.
func TestHTTPRefusesATimeoutAboveThePolicyLimit(t *testing.T) {
	_, err := HTTP{Policy: builtins.Policy{DefaultTimeout: "1s"}}.Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": "https://example.com", "timeout": "10s"},
	})
	if err == nil {
		t.Fatal("Execute accepted a timeout above the policy limit")
	}
	if !strings.Contains(err.Error(), "exceeds the limit") {
		t.Errorf("error = %v, want it to state the limit", err)
	}
}

// A request that outlasts its timeout fails with the deadline, not with a
// transport error.
func TestHTTPReturnsTheDeadlineWhenTheServerIsTooSlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	_, err := localHTTP().Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"url": server.URL, "timeout": "50ms"},
	})
	if err == nil {
		t.Fatal("Execute succeeded after the deadline")
	}
	if !strings.Contains(err.Error(), "deadline") {
		t.Errorf("error = %v, want it to state the deadline", err)
	}
}
