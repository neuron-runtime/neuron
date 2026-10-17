package sourceregistry

import "testing"

func TestAddGetNames(t *testing.T) {
	r := New[string]()
	if err := r.Add("local", "L"); err != nil {
		t.Fatalf("add local: %v", err)
	}
	if err := r.Add("github", "G"); err != nil {
		t.Fatalf("add github: %v", err)
	}
	if v, ok := r.Get("local"); !ok || v != "L" {
		t.Fatalf("Get(local) = %q, %v", v, ok)
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatal("Get(missing) must miss")
	}
	names := r.Names()
	if len(names) != 2 || names[0] != "github" || names[1] != "local" {
		t.Fatalf("Names() = %v, want [github local]", names)
	}
}

func TestAddRejectsDuplicateAndEmpty(t *testing.T) {
	r := New[int]()
	if err := r.Add("a", 1); err != nil {
		t.Fatalf("add a: %v", err)
	}
	if err := r.Add("a", 2); err == nil {
		t.Fatal("duplicate add must fail")
	}
	if err := r.Add("  ", 3); err == nil {
		t.Fatal("empty name must fail")
	}
}
