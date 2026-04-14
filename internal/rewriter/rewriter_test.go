package rewriter

import "testing"

func TestApply_NoPatches(t *testing.T) {
	src := "SELECT 1"
	out, warn := Apply(src, nil)
	if out != src {
		t.Error("source should be unchanged")
	}
	if warn != nil {
		t.Error("no warnings expected")
	}
}

func TestApply_Single(t *testing.T) {
	src := "SELECT * FROM t"
	out, warn := Apply(src, []Patch{
		{Start: 7, End: 8, Replacement: "id, name", Rule: "select_star"},
	})
	want := "SELECT id, name FROM t"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
	if len(warn) != 0 {
		t.Errorf("unexpected warnings: %v", warn)
	}
}

func TestApply_Multiple_NonOverlapping(t *testing.T) {
	src := "ABCDEFGH"
	out, _ := Apply(src, []Patch{
		{Start: 1, End: 2, Replacement: "-"},
		{Start: 5, End: 6, Replacement: "+"},
	})
	want := "A-CDE+GH"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestApply_OverlappingDropped(t *testing.T) {
	src := "ABCDEFGH"
	_, warn := Apply(src, []Patch{
		{Start: 2, End: 5, Replacement: "xxx", Rule: "r1"},
		{Start: 3, End: 7, Replacement: "yyy", Rule: "r2"},
	})
	if len(warn) == 0 {
		t.Error("expected a warning about overlap")
	}
}

func TestApply_InvalidSkipped(t *testing.T) {
	src := "ABC"
	_, warn := Apply(src, []Patch{
		{Start: -1, End: 2, Replacement: "x", Rule: "r"},
	})
	if len(warn) == 0 {
		t.Error("expected warning for invalid range")
	}
}
