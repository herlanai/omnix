package sysprompt

import "testing"

func TestSectionsOrderThenName(t *testing.T) {
	a := New()
	// Register equal-order sections in deliberately scrambled order.
	a.Register("zeta", 100, "z")
	a.Register("alpha", 100, "a")
	a.Register("first", 10, "f")
	got := a.Sections()
	names := []string{got[0].Name, got[1].Name, got[2].Name}
	if names[0] != "first" || names[1] != "alpha" || names[2] != "zeta" {
		t.Fatalf("order = %v", names)
	}
}
