package assist

import (
	"errors"
	"testing"
)

func TestRungValid(t *testing.T) {
	for r := RungConstraint; r <= RungOutline; r++ {
		if !r.Valid() {
			t.Errorf("Rung(%d).Valid() = false, want true", r)
		}
	}
	for _, r := range []Rung{-1, 0, 5, 99} {
		if r.Valid() {
			t.Errorf("Rung(%d).Valid() = true, want false", r)
		}
	}
}

func TestRungString(t *testing.T) {
	seen := map[string]bool{}
	for r := RungConstraint; r <= RungOutline; r++ {
		s := r.String()
		if s == "" {
			t.Fatalf("Rung(%d).String() is empty", r)
		}
		if seen[s] {
			t.Fatalf("Rung(%d).String() = %q, which is already taken", r, s)
		}
		seen[s] = true
	}
	if Rung(9).String() == "" {
		t.Fatal("an out-of-range rung must still stringify for logs")
	}
}

// TestSentinelsAreDistinct: the controller maps each of these to a
// different HTTP status, so none may match another under errors.Is.
func TestSentinelsAreDistinct(t *testing.T) {
	all := []error{ErrDisabled, ErrUnavailable, ErrFiltered, ErrLeak, ErrInvalidRung}

	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Errorf("sentinel %d matches sentinel %d", i, j)
			}
		}
	}
}
