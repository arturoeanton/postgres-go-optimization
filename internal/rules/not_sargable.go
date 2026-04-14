package rules

import (
	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// notSargable flags `WHERE col OP const OP value` patterns where the
// indexed column is buried under arithmetic — the classic non-sargable
// predicate. Example: `WHERE col + 1 > 100` — rewrite as `col > 99`.
//
// The planner cannot push arithmetic into an index scan; it would have
// to evaluate `col + 1` for every row before comparing. Common offenders:
//   - col + N op X
//   - col - N op X
//   - col * N op X
//   - col || 'suffix' = '...'
type notSargable struct{}

func (notSargable) ID() string                        { return "not_sargable" }
func (notSargable) Description() string               { return "Arithmetic or concatenation on an indexed column blocks index usage" }
func (notSargable) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (notSargable) RequiresSchema() bool              { return false }
func (notSargable) RequiresExplain() bool             { return false }

func (notSargable) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	walkWhereClauses(ctx.AST, func(where analyzer.ASTNode) {
		analyzer.Walk(where, func(path []string, n analyzer.ASTNode) bool {
			if analyzer.NodeKind(n) != "A_Expr" {
				return true
			}
			e := analyzer.Inner(n)
			if analyzer.AsString(e, "kind") != "AEXPR_OP" {
				return true
			}
			// lexpr is another A_Expr (arithmetic/concat) whose children include a ColumnRef.
			l := analyzer.AsMap(e, "lexpr")
			if analyzer.NodeKind(l) != "A_Expr" {
				return true
			}
			inner := analyzer.Inner(l)
			op := extractOpName(analyzer.AsList(inner, "name"))
			if op != "+" && op != "-" && op != "*" && op != "/" && op != "||" {
				return true
			}
			// Does one side of the arithmetic refer to a ColumnRef?
			ll := analyzer.AsMap(inner, "lexpr")
			rr := analyzer.AsMap(inner, "rexpr")
			var col analyzer.ASTNode
			if analyzer.NodeKind(ll) == "ColumnRef" {
				col = ll
			} else if analyzer.NodeKind(rr) == "ColumnRef" {
				col = rr
			} else {
				return true
			}
			colName := extractColumnName(col)
			loc := analyzer.AsInt(inner, "location")
			out = append(out, analyzer.Finding{
				Severity: analyzer.SeverityWarn,
				Message:  "Non-sargable predicate: `" + colName + " " + op + " ...` wrapped in a comparison prevents index usage on `" + colName + "`",
				Explanation: "For a btree index to be used, the indexed column must appear as an operand of the " +
					"comparison (match_index_to_operand in src/backend/optimizer/path/indxpath.c). Arithmetic on " +
					"the indexed side moves the column under an expression, which the planner cannot push down.",
				Suggestion: "Algebraically move the constant to the other side (e.g. `col + 1 > 100` ⇒ `col > 99`).",
				Evidence:   "src/backend/optimizer/path/indxpath.c; GUIA_POSTGRES_ES_2.md §35.1",
				Location:   analyzer.Range{Start: loc, End: loc + 1},
			})
			return true
		})
	})
	return out
}

func extractOpName(names []any) string {
	for _, nm := range names {
		if mm, ok := nm.(analyzer.ASTNode); ok {
			if s := analyzer.AsString(analyzer.Inner(mm), "sval"); s != "" {
				return s
			}
		}
	}
	return ""
}

func init() { Register(notSargable{}) }
