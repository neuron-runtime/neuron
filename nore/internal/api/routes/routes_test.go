package routes

import (
	"net/http"
	"testing"
)

// TestBuildRoutesRegistersWithoutConflict guards the route table's HTTP
// pattern graph. Go's ServeMux panics when two registered patterns conflict
// (for example a literal and a wildcard that neither dominate nor exclude one
// another), and that panic only fires at startup, so an unguarded new route
// turns into a runtime crash rather than a test failure.
func TestBuildRoutesDoesNotPanic(t *testing.T) {
	mux := http.NewServeMux()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("BuildRoutes panicked registering routes: %v", r)
		}
	}()
	_ = BuildRoutes(mux, nil, nil, nil, "")
}
