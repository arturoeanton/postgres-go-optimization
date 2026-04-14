package analyzer

import "testing"

func TestParsePragmas_File(t *testing.T) {
	src := "-- pgopt:ignore=foo,bar\nSELECT 1"
	p := ParsePragmas(src)
	if !p.File["foo"] || !p.File["bar"] {
		t.Errorf("expected foo and bar silenced: %+v", p.File)
	}
}

func TestParsePragmas_IgnoreNextTargetsNextCodeLine(t *testing.T) {
	src := "-- pgopt:ignore-next\n\n-- another comment\nSELECT * FROM t"
	p := ParsePragmas(src)
	if len(p.IgnoreNext) != 1 {
		t.Fatalf("want 1 ignore-next range, got %d", len(p.IgnoreNext))
	}
	// The targeted line is "SELECT * FROM t" which starts after the two
	// skipped lines.
	r := p.IgnoreNext[0]
	if src[r.start:r.end] != "SELECT * FROM t" {
		t.Errorf("ignore-next targeted wrong span: %q", src[r.start:r.end])
	}
}

func TestParsePragmas_IgnoresMalformed(t *testing.T) {
	src := "-- pgopt:gibberish\n-- pgopt:ignore\nSELECT 1"
	p := ParsePragmas(src)
	if len(p.File) != 0 || len(p.IgnoreNext) != 0 {
		t.Errorf("malformed pragmas should be ignored: %+v", p)
	}
}

func TestFilterFindings_ByRule(t *testing.T) {
	p := Pragmas{File: map[string]bool{"a": true}}
	fs := FindingList{
		{Rule: "a", Message: "silenced"},
		{Rule: "b", Message: "visible"},
	}
	out := FilterFindings(fs, p)
	if len(out) != 1 || out[0].Rule != "b" {
		t.Errorf("expected only rule 'b' to survive: %+v", out)
	}
}

func TestFilterFindings_ByIgnoreNext(t *testing.T) {
	src := "-- pgopt:ignore-next\nSELECT * FROM t"
	p := ParsePragmas(src)
	// Finding at offset of "SELECT" (== 22) should be silenced.
	fs := FindingList{
		{Rule: "select_star", Location: Range{Start: 22, End: 23}, Message: "x"},
		{Rule: "other", Location: Range{Start: 0, End: 1}, Message: "y"},
	}
	out := FilterFindings(fs, p)
	for _, f := range out {
		if f.Rule == "select_star" {
			t.Error("ignore-next should have silenced select_star")
		}
	}
}

func TestFilterFindings_NoopWhenNoPragmas(t *testing.T) {
	fs := FindingList{{Rule: "a"}}
	out := FilterFindings(fs, Pragmas{})
	if len(out) != 1 {
		t.Error("empty pragmas should not filter anything")
	}
}
