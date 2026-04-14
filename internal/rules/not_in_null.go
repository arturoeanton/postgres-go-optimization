package rules

import (
	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// notInNull flags `NOT IN (subquery)` and recommends `NOT EXISTS`.
//
// The SQL `x NOT IN (SELECT y ...)` is represented by the PostgreSQL parser
// as a BoolExpr (boolop=NOT_EXPR) wrapping a SubLink (subLinkType=ANY_SUBLINK).
// Alternative (rare) form: SubLink with ALL_SUBLINK and operName '<>'.
//
// The pitfall: NOT IN uses three-valued logic — if any right-hand value is
// NULL, the boolean is UNKNOWN, which acts like FALSE in WHERE and filters
// out EVERY row. NOT EXISTS is row-by-row existence and immune to this.
type notInNull struct{}

func (notInNull) ID() string                        { return "not_in_null" }
func (notInNull) Description() string               { return "NOT IN with subquery returns UNKNOWN on NULL; prefer NOT EXISTS" }
func (notInNull) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (notInNull) RequiresSchema() bool              { return false }
func (notInNull) RequiresExplain() bool             { return false }

func (notInNull) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	analyzer.Walk(ctx.AST, func(path []string, n analyzer.ASTNode) bool {
		if analyzer.NodeKind(n) != "BoolExpr" {
			return true
		}
		b := analyzer.Inner(n)
		if analyzer.AsString(b, "boolop") != "NOT_EXPR" {
			return true
		}
		args := analyzer.AsList(b, "args")
		if len(args) != 1 {
			return true
		}
		arg, _ := args[0].(analyzer.ASTNode)
		if analyzer.NodeKind(arg) != "SubLink" {
			return true
		}
		sl := analyzer.Inner(arg)
		if analyzer.AsString(sl, "subLinkType") != "ANY_SUBLINK" {
			return true
		}
		loc := analyzer.AsInt(b, "location")
		out = append(out, analyzer.Finding{
			Severity: analyzer.SeverityWarn,
			Message:  "NOT IN (subquery) is unsafe with NULLs: any NULL on the right filters every row",
			Explanation: "NOT IN uses three-valued logic: `x <> ALL(subquery)`. If the subquery ever produces a NULL, " +
				"the boolean result becomes UNKNOWN, which behaves like FALSE in WHERE. NOT EXISTS evaluates " +
				"existence per outer row and cannot misbehave on NULLs.",
			Suggestion: "Rewrite: `... WHERE NOT EXISTS (SELECT 1 FROM banned_users b WHERE b.user_id = u.id)`.",
			Evidence:   "src/backend/optimizer/plan/subselect.c (sublink handling); SQL standard 3VL semantics",
			Location:   analyzer.Range{Start: loc, End: loc + 1},
		})
		return true
	})
	return out
}

func init() { Register(notInNull{}) }
