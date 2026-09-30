package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/neuron-runtime/neuron/shared/types/apitoken"
)

// TokenAuth rejects requests that do not present N.O.R.E.'s API token.
//
// N.O.R.E.'s API can register assemblies, create instances, and execute
// capability runtimes, so it is an authority to run code. A local Unix socket
// is reachable by every process on the machine, which makes the socket a
// convenience rather than a boundary. The token is what makes the API an
// authenticated boundary, and is the precondition for exposing it over TCP.
//
// The credential is compared in constant time so a rejected token cannot be
// recovered a byte at a time by measuring the comparison.
//
// /health is exempt: a liveness probe must answer without a credential, and it
// reveals nothing beyond the fact that the process is running.
type TokenAuth struct {
	// token is the expected bearer token. An empty token disables
	// authentication, which the daemon permits only for a socket-scoped
	// listener and refuses for a TCP listener.
	token string

	// healthPath is served without authentication.
	healthPath string
}

// NewTokenAuth returns middleware that requires the given token on every route
// except the health probe. An empty token produces a no-op chain: the caller
// has already established that the endpoint is trusted, and pretending to
// authenticate would only hide that decision.
func NewTokenAuth(token string) *TokenAuth {
	return &TokenAuth{token: token, healthPath: "/health"}
}

// Wrap returns a handler that authenticates before delegating to next.
func (a *TokenAuth) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.authorized(r) {
			// Do not describe why: a rejection should not confirm which part of
			// the credential was wrong.
			w.Header().Set("WWW-Authenticate", `Bearer realm="neuron"`)
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authorized reports whether the request is allowed through. Authentication is
// skipped when no token is configured, and for the health probe.
func (a *TokenAuth) authorized(r *http.Request) bool {
	if a == nil || a.token == "" {
		return true
	}
	if a.healthPath != "" && r.URL.Path == a.healthPath {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(a.presentedToken(r)), []byte(a.token)) == 1
}

// presentedToken extracts the bearer credential from the request's
// Authorization header.
func (a *TokenAuth) presentedToken(r *http.Request) string {
	const prefix = "Bearer "
	value := r.Header.Get(apitoken.AuthorizationHeader)
	if len(value) <= len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(value[len(prefix):])
}
