package rules

import (
	"fmt"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// largeInList flags `WHERE col IN (v1, v2, ..., vN)` with N above a
// threshold, suggesting a VALUES join or temp table for better planning.
//
// Very large IN lists bloat the parse tree, the jumble, and the plan
// cache, and the planner evaluates predicates as a disjunction of
// equalities which is slower than a hash semijoin on a VALUES relation.
type largeInList struct{}

const largeInThreshold = 100

func (largeInList) ID() string                        { return "large_in_list" }
func (largeInList) Description() string               { return "Large IN (...) list: prefer JOIN against VALUES or a temp table" }
func (largeInList) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (largeInList) RequiresSchema() bool              { return false }
func (largeInList) RequiresExplain() bool             { return false }

func (largeInList) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	analyzer.Walk(ctx.AST, func(path []string, n analyzer.ASTNode) bool {
		if analyzer.NodeKind(n) != "A_Expr" {
			return true
		}
		e := analyzer.Inner(n)
		if analyzer.AsString(e, "kind") != "AEXPR_IN" {
			return true
		}
		// rexpr for `col IN (lit1, lit2, ...)` is a List node: {"List": {"items": [...]}}.
		r := analyzer.AsMap(e, "rexpr")
		if r == nil {
			return true
		}
		var items []any
		if analyzer.NodeKind(r) == "List" {
			items = analyzer.AsList(analyzer.Inner(r), "items")
		} else if l, ok := e["rexpr"].([]any); ok {
			items = l
		}
		if len(items) < largeInThreshold {
			return true
		}
		list := items
		loc := analyzer.AsInt(e, "location")
		out = append(out, analyzer.Finding{
			Severity: analyzer.SeverityWarn,
			Message: fmt.Sprintf("IN list has %d items: consider JOIN against VALUES or a temp table",
				len(list)),
			Explanation: "A large IN list produces a long disjunction of equality checks. Wrapping the ids in a " +
				"VALUES relation or a temp table lets the planner pick a hash semijoin (nodeHashjoin.c), which " +
				"scales much better. It also keeps the query plan cache healthy (different sizes produce different plans).",
			Suggestion: "Rewrite: ... FROM t JOIN (VALUES (1),(2),...) v(id) USING (id); " +
				"or CREATE TEMP TABLE and COPY the ids.",
			Evidence: "src/backend/executor/nodeHashjoin.c; GUIA_POSTGRES_ES_2.md §35.4",
			Location: analyzer.Range{Start: loc, End: loc + 1},
		})
		return true
	})
	return out
}

func init() { Register(largeInList{}) }
