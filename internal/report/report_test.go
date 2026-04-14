package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

func findings() analyzer.FindingList {
	return analyzer.FindingList{
		{
			Rule:     "demo",
			Severity: analyzer.SeverityWarn,
			Message:  "hello",
			Location: analyzer.Range{Start: 0, End: 1},
		},
	}
}

func TestText_Empty(t *testing.T) {
	var b bytes.Buffer
	Text(&b, nil, TextOptions{})
	if !strings.Contains(b.String(), "no findings") {
		t.Errorf("want 'no findings', got %q", b.String())
	}
}

func TestText_Basic(t *testing.T) {
	var b bytes.Buffer
	Text(&b, findings(), TextOptions{Source: "SELECT *"})
	s := b.String()
	for _, want := range []string{"[WARN]", "demo", "hello", "1 finding"} {
		if !strings.Contains(s, want) {
			t.Errorf("text output missing %q:\n%s", want, s)
		}
	}
}

func TestText_ColorEscapes(t *testing.T) {
	var b bytes.Buffer
	Text(&b, findings(), TextOptions{Color: true})
	if !strings.Contains(b.String(), "\x1b[") {
		t.Error("expected ANSI escape with color=true")
	}
}

func TestText_VerboseIncludesExplanationAndEvidence(t *testing.T) {
	fs := analyzer.FindingList{
		{
			Rule:        "demo",
			Severity:    analyzer.SeverityError,
			Message:     "bad",
			Explanation: "because planner",
			Suggestion:  "fix it",
			Evidence:    "src/foo.c:42",
			Snippet:     "snip",
			Location:    analyzer.Range{Start: 7, End: 8},
		},
	}
	var b bytes.Buffer
	Text(&b, fs, TextOptions{Verbose: true, Source: "SELECT * FROM t"})
	s := b.String()
	for _, want := range []string{"[ERROR]", "because planner", "fix it", "src/foo.c:42", "snip"} {
		if !strings.Contains(s, want) {
			t.Errorf("verbose output missing %q:\n%s", want, s)
		}
	}
}

func TestText_InfoSeverityAndLineCol(t *testing.T) {
	fs := analyzer.FindingList{
		{
			Rule:     "info_demo",
			Severity: analyzer.SeverityInfo,
			Message:  "note",
			Location: analyzer.Range{Start: 17, End: 18}, // line 2
		},
	}
	var b bytes.Buffer
	Text(&b, fs, TextOptions{Source: "line1 padding\nline2 here", Color: true})
	s := b.String()
	if !strings.Contains(s, "[INFO]") {
		t.Errorf("info label missing: %s", s)
	}
	if !strings.Contains(s, "at 2:") {
		t.Errorf("expected 'at 2:' for second line, got:\n%s", s)
	}
}

func TestText_MultipleFindingsHaveBlankLineBetween(t *testing.T) {
	fs := analyzer.FindingList{
		{Rule: "a", Severity: analyzer.SeverityWarn, Message: "m1", Suggestion: "s1"},
		{Rule: "b", Severity: analyzer.SeverityError, Message: "m2", Suggestion: "s2"},
	}
	var b bytes.Buffer
	Text(&b, fs, TextOptions{Source: "SELECT 1"})
	out := b.String()
	if !strings.Contains(out, "\n\n") {
		t.Errorf("expected blank line between findings:\n%s", out)
	}
	if !strings.Contains(out, "2 finding(s): 1 error, 1 warn, 0 info") {
		t.Errorf("summary line wrong:\n%s", out)
	}
}

func TestText_SuggestionWraps(t *testing.T) {
	long := strings.Repeat("word ", 40)
	fs := analyzer.FindingList{{
		Rule: "a", Severity: analyzer.SeverityWarn, Message: "m",
		Suggestion: long,
	}}
	var b bytes.Buffer
	Text(&b, fs, TextOptions{Source: "SELECT 1"})
	// A wrapped line should contain the continuation indent.
	if !strings.Contains(b.String(), "\n         ") {
		t.Errorf("expected continuation indent in wrapped suggestion:\n%s", b.String())
	}
}

func TestText_SnippetTruncatedAt120(t *testing.T) {
	fs := analyzer.FindingList{{
		Rule: "x", Severity: analyzer.SeverityWarn, Message: "m",
		Snippet: strings.Repeat("a", 200),
	}}
	var b bytes.Buffer
	Text(&b, fs, TextOptions{Source: ""})
	if !strings.Contains(b.String(), "…") {
		t.Errorf("expected truncation marker when snippet exceeds 120 chars:\n%s", b.String())
	}
}

func TestText_EmptyColorUsesGreenOK(t *testing.T) {
	var b bytes.Buffer
	Text(&b, nil, TextOptions{Color: true})
	if !strings.Contains(b.String(), "\x1b[32") {
		t.Errorf("expected green escape for clean-run banner:\n%q", b.String())
	}
}

func TestLineCol_EdgeCases(t *testing.T) {
	src := "a\nb\nc"
	tests := []struct{ off, wantLine, wantCol int }{
		{-1, 1, 1},
		{0, 1, 1},
		{2, 2, 1},
		{100, 1, 1}, // out of range → 1,1 per implementation
	}
	for _, tc := range tests {
		gl, gc := lineCol(src, tc.off)
		if gl != tc.wantLine || gc != tc.wantCol {
			t.Errorf("lineCol(%d)=%d,%d want %d,%d", tc.off, gl, gc, tc.wantLine, tc.wantCol)
		}
	}
}

func TestJSON_Structure(t *testing.T) {
	var b bytes.Buffer
	if err := JSON(&b, findings()); err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v["findings"] == nil {
		t.Error("missing findings")
	}
	sum, ok := v["summary"].(map[string]any)
	if !ok {
		t.Fatal("summary wrong type")
	}
	if sum["total"].(float64) != 1 || sum["warn"].(float64) != 1 {
		t.Errorf("summary numbers wrong: %v", sum)
	}
}
