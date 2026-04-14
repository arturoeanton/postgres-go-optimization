package analyzer

import (
	"sort"
	"testing"
)

func TestSeverityString(t *testing.T) {
	cases := map[Severity]string{
		SeverityInfo:  "info",
		SeverityWarn:  "warn",
		SeverityError: "error",
	}
	for s, want := range cases {
		if s.String() != want {
			t.Errorf("%v.String() = %q, want %q", s, s.String(), want)
		}
	}
}

func TestParseSeverity(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Severity
		err  bool
	}{
		{"info", SeverityInfo, false},
		{"warn", SeverityWarn, false},
		{"warning", SeverityWarn, false},
		{"error", SeverityError, false},
		{"x", 0, true},
	} {
		got, err := ParseSeverity(tc.in)
		if (err != nil) != tc.err {
			t.Errorf("%q: err=%v want err=%v", tc.in, err, tc.err)
		}
		if got != tc.want {
			t.Errorf("%q: got %v want %v", tc.in, got, tc.want)
		}
	}
}

func TestFindingListSort(t *testing.T) {
	f := FindingList{
		{Severity: SeverityInfo, Location: Range{Start: 10}, Rule: "a"},
		{Severity: SeverityError, Location: Range{Start: 50}, Rule: "b"},
		{Severity: SeverityWarn, Location: Range{Start: 30}, Rule: "c"},
		{Severity: SeverityError, Location: Range{Start: 5}, Rule: "d"},
	}
	sort.Sort(f)
	// Errors first (sorted by start); then warn; then info.
	if f[0].Rule != "d" {
		t.Errorf("want d first, got %q", f[0].Rule)
	}
	if f[1].Rule != "b" {
		t.Errorf("want b second, got %q", f[1].Rule)
	}
	if f[2].Rule != "c" {
		t.Errorf("want c third, got %q", f[2].Rule)
	}
	if f[3].Rule != "a" {
		t.Errorf("want a last, got %q", f[3].Rule)
	}
}

func TestFindingListMaxSeverity(t *testing.T) {
	f := FindingList{
		{Severity: SeverityInfo},
		{Severity: SeverityWarn},
	}
	if f.MaxSeverity() != SeverityWarn {
		t.Error("max severity wrong")
	}
}

func TestContextSnippet(t *testing.T) {
	c := &Context{Source: "SELECT * FROM t"}
	s := c.Snippet(Range{Start: 7, End: 8})
	if s != "*" {
		t.Errorf("got %q", s)
	}
	// out-of-range
	if c.Snippet(Range{Start: -1, End: 5}) != "" {
		t.Error("expected empty on invalid range")
	}
}

func TestContextLineCol(t *testing.T) {
	c := &Context{Source: "line1\nline2\nxyz"}
	line, col := c.LineCol(12)
	if line != 3 || col != 1 {
		t.Errorf("got %d:%d, want 3:1", line, col)
	}
}
