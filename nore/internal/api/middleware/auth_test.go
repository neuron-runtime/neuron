package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestTokenAuthRejectsRequestsWithoutCredential(t *testing.T) {
	handler := NewTokenAuth("secret").Wrap(okHandler())

	cases := map[string]*http.Request{
		"no header":       httptest.NewRequest(http.MethodGet, "/v1/instances", nil),
		"wrong token":     requestWithToken(t, "nope"),
		"empty bearer":    requestWithHeader(t, "Bearer "),
		"wrong scheme":    requestWithHeader(t, "Basic secret"),
		"token only":      requestWithHeader(t, "secret"),
		"trailing scheme": requestWithHeader(t, "Bearer secret Bearer"),
	}

	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer realm="neuron"` {
				t.Fatalf("WWW-Authenticate = %q", got)
			}
		})
	}
}

func TestTokenAuthAcceptsBearerToken(t *testing.T) {
	handler := NewTokenAuth("secret").Wrap(okHandler())

	for name, value := range map[string]string{
		"canonical":  "Bearer secret",
		"lowercase":  "bearer secret",
		"padded":     "Bearer   secret  ",
		"withScheme": "BEARER secret",
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, requestWithHeader(t, value))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
		})
	}
}

func TestTokenAuthExemptsHealthProbe(t *testing.T) {
	handler := NewTokenAuth("secret").Wrap(okHandler())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200 without a credential", rec.Code)
	}
}

func TestTokenAuthDoesNotExemptLookalikeHealthPaths(t *testing.T) {
	handler := NewTokenAuth("secret").Wrap(okHandler())

	for _, path := range []string{"/healthz", "/health/", "/v1/health", "/HEALTH"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", path, rec.Code)
		}
	}
}

func TestTokenAuthIsInertWithoutConfiguredToken(t *testing.T) {
	handler := NewTokenAuth("").Wrap(okHandler())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/instances", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an unauthenticated daemon", rec.Code)
	}
}

func requestWithToken(t *testing.T, token string) *http.Request {
	t.Helper()
	return requestWithHeader(t, "Bearer "+token)
}

func requestWithHeader(t *testing.T, value string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/instances", nil)
	req.Header.Set("Authorization", value)
	return req
}
