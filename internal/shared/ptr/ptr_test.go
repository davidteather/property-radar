package ptr_test

import (
	"testing"

	"github.com/davidteather/property-radar/internal/shared/ptr"
)

func TestTo(t *testing.T) {
	if p := ptr.To(42); *p != 42 {
		t.Fatalf("To(42) = %d", *p)
	}
}

func TestDeref(t *testing.T) {
	if got := ptr.Deref[int](nil); got != 0 {
		t.Fatalf("Deref(nil) = %d, want zero", got)
	}
	if got := ptr.Deref(ptr.To("x")); got != "x" {
		t.Fatalf("Deref = %q", got)
	}
}

func TestClone(t *testing.T) {
	if ptr.Clone[int](nil) != nil {
		t.Fatal("Clone(nil) != nil")
	}
	orig := ptr.To(7)
	c := ptr.Clone(orig)
	*c = 8
	if *orig != 7 {
		t.Fatalf("Clone aliased the original: %d", *orig)
	}
}

func TestFirst(t *testing.T) {
	if ptr.First[int]() != nil || ptr.First[int](nil, nil) != nil {
		t.Fatal("First of nils should be nil")
	}
	if got := ptr.First(nil, ptr.To(1), ptr.To(2)); *got != 1 {
		t.Fatalf("First = %d, want 1", *got)
	}
}
