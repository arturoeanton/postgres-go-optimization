package analyzer

import (
	"testing"

	"github.com/arturoeanton/postgres-go-optimization/internal/explain"
	"github.com/arturoeanton/postgres-go-optimization/internal/schema"
)

type fakeRule struct {
	id      string
	schema  bool
	explain bool
	fn      func(*Context) []Finding
}

func (r fakeRule) ID() string              { return r.id }
func (r fakeRule) RequiresSchema() bool    { return r.schema }
func (r fakeRule) RequiresExplain() bool   { return r.explain }
func (r fakeRule) Check(c *Context) []Finding { return r.fn(c) }

func TestRun_StampsRuleIDAndSnippet(t *testing.T) {
	r := fakeRule{
		id: "demo",
		fn: func(c *Context) []Finding {
			return []Finding{{
				Severity: SeverityWarn,
				Message:  "m",
				Location: Range{Start: 0, End: 6},
			}}
		},
	}
	ctx := &Context{Source: "SELECT 1", AST: ASTNode{}}
	out := Run(ctx, []Runnable{r})
	if len(out) != 1 {
		t.Fatalf("want 1 finding, got %d", len(out))
	}
	if out[0].Rule != "demo" {
		t.Errorf("Rule not stamped: %q", out[0].Rule)
	}
	if out[0].Snippet != "SELECT" {
		t.Errorf("Snippet should be derived from Range: %q", out[0].Snippet)
	}
}

func TestRun_SkipsUnmetRequirements(t *testing.T) {
	called := 0
	schemaRule := fakeRule{id: "s", schema: true, fn: func(*Context) []Finding {
		called++
		return []Finding{{Message: "should not run"}}
	}}
	explainRule := fakeRule{id: "e", explain: true, fn: func(*Context) []Finding {
		called++
		return []Finding{{Message: "should not run"}}
	}}
	ctx := &Context{Source: "SELECT 1", AST: ASTNode{}}
	out := Run(ctx, []Runnable{schemaRule, explainRule})
	if called != 0 {
		t.Errorf("rules with unmet requirements should not be invoked, got %d calls", called)
	}
	if len(out) != 0 {
		t.Errorf("unexpected findings: %+v", out)
	}
}

func TestRun_SortsBySeverityThenLocation(t *testing.T) {
	r := fakeRule{id: "x", fn: func(*Context) []Finding {
		return []Finding{
			{Severity: SeverityInfo, Message: "third", Location: Range{Start: 0, End: 1}},
			{Severity: SeverityError, Message: "first", Location: Range{Start: 5, End: 6}},
			{Severity: SeverityWarn, Message: "second", Location: Range{Start: 2, End: 3}},
		}
	}}
	out := Run(&Context{Source: "0123456789"}, []Runnable{r})
	got := []string{out[0].Message, out[1].Message, out[2].Message}
	want := []string{"first", "second", "third"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("sort order [%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestRun_WithSchemaAndExplainContexts(t *testing.T) {
	called := 0
	r := fakeRule{id: "needs_both", schema: true, explain: true, fn: func(c *Context) []Finding {
		called++
		if c.Schema == nil || c.Explain == nil {
			t.Error("expected both Schema and Explain to be set")
		}
		return nil
	}}
	ctx := &Context{
		Source:  "SELECT 1",
		AST:     ASTNode{},
		Schema:  &schema.Schema{},
		Explain: &explain.Plan{Root: &explain.Node{NodeType: "Result"}},
	}
	Run(ctx, []Runnable{r})
	if called != 1 {
		t.Error("rule with all requirements met must run")
	}
}

func TestParseJSON_InvalidSQL(t *testing.T) {
	if _, err := ParseJSON("SELEKT 1 FROM"); err == nil {
		t.Error("expected parse error on invalid SQL")
	}
}

func TestWalker_Helpers(t *testing.T) {
	// Exercise every accessor on a synthetic tree — covers the nil and
	// wrong-type branches that rules tests rarely hit.
	if NodeKind(ASTNode{"A": 1, "B": 2}) != "" {
		t.Error("NodeKind should return empty string for multi-keyed node")
	}
	if Inner(ASTNode{"A": 1, "B": 2}) != nil {
		t.Error("Inner should return nil for multi-keyed node")
	}
	if Inner(ASTNode{"A": "not a map"}) != nil {
		t.Error("Inner should return nil when value is not a map")
	}
	if AsString(nil, "k") != "" {
		t.Error("AsString(nil) must be empty")
	}
	if AsInt(nil, "k") != 0 {
		t.Error("AsInt(nil) must be 0")
	}
	if AsList(nil, "k") != nil {
		t.Error("AsList(nil) must be nil")
	}
	if AsMap(nil, "k") != nil {
		t.Error("AsMap(nil) must be nil")
	}
	// wrong-type branches
	m := ASTNode{"s": 123, "i": "x", "l": "x", "m": "x"}
	if AsString(m, "s") != "" {
		t.Error("AsString should return empty when value is not a string")
	}
	if AsInt(m, "i") != 0 {
		t.Error("AsInt should return 0 when value is not numeric")
	}
	if AsList(m, "l") != nil {
		t.Error("AsList should return nil when value is not a list")
	}
	if AsMap(m, "m") != nil {
		t.Error("AsMap should return nil when value is not a map")
	}
	if AsInt(ASTNode{"i": 7}, "i") != 7 {
		t.Error("AsInt should accept int directly")
	}
}

func TestContextLineCol_OutOfRange(t *testing.T) {
	c := &Context{Source: "line1\nline2"}
	l, col := c.LineCol(-5)
	if l != 1 || col != 1 {
		t.Errorf("negative offset should clamp to 1,1: got %d,%d", l, col)
	}
	l, col = c.LineCol(10_000)
	if l != 1 || col != 1 {
		t.Errorf("large offset should clamp to 1,1: got %d,%d", l, col)
	}
}

func TestSeverity_StringAndParse(t *testing.T) {
	for _, s := range []Severity{SeverityInfo, SeverityWarn, SeverityError} {
		name := s.String()
		parsed, err := ParseSeverity(name)
		if err != nil || parsed != s {
			t.Errorf("roundtrip failed for %v: got %v err=%v", s, parsed, err)
		}
	}
	if Severity(99).String() != "unknown" {
		t.Error("out-of-range severity should stringify as 'unknown'")
	}
	if _, err := ParseSeverity("bogus"); err == nil {
		t.Error("unknown severity should error")
	}
	// "warning" alias
	if s, err := ParseSeverity("warning"); err != nil || s != SeverityWarn {
		t.Errorf("'warning' alias broken: got %v err=%v", s, err)
	}
}

func TestSnippet_OutOfRange(t *testing.T) {
	c := &Context{Source: "abc"}
	if c.Snippet(Range{Start: -1, End: 2}) != "" {
		t.Error("negative start should return empty")
	}
	if c.Snippet(Range{Start: 2, End: 1}) != "" {
		t.Error("end <= start should return empty")
	}
	if c.Snippet(Range{Start: 0, End: 100}) != "" {
		t.Error("end > len should return empty")
	}
	if c.Snippet(Range{Start: 0, End: 3}) != "abc" {
		t.Error("valid range should return slice")
	}
}

func TestMaxSeverity(t *testing.T) {
	fs := FindingList{
		{Severity: SeverityInfo},
		{Severity: SeverityWarn},
		{Severity: SeverityError},
	}
	if fs.MaxSeverity() != SeverityError {
		t.Error("MaxSeverity should return the highest")
	}
	if (FindingList{}).MaxSeverity() != SeverityInfo {
		t.Error("empty list MaxSeverity should default to Info")
	}
}
