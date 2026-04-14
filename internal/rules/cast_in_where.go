package rules

import (
	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// castInWhere warns about `WHERE col::type = value` patterns.
//
// A cast applied to an indexed column prevents the planner from using an
// index on that column, unless the index is defined on the same expression
// (`CREATE INDEX ... ((col::type))`). This is a well-known cause of
// "index not used" mysteries.
//
// Example: WHERE created_at::date = '2026-01-01' cannot use an index on
// created_at (timestamptz). Rewrite as a range: created_at >= '2026-01-01'
// AND created_at < '2026-01-02'.
type castInWhere struct{}

func (castInWhere) ID() string                        { return "cast_in_where" }
func (castInWhere) Description() string               { return "Cast applied to a column in WHERE disables indexes on that column" }
func (castInWhere) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (castInWhere) RequiresSchema() bool              { return false }
func (castInWhere) RequiresExplain() bool             { return false }

func (castInWhere) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	walkWhereClauses(ctx.AST, func(where analyzer.ASTNode) {
		analyzer.Walk(where, func(path []string, n analyzer.ASTNode) bool {
			if analyzer.NodeKind(n) != "TypeCast" {
				return true
			}
			tc := analyzer.Inner(n)
			arg := analyzer.AsMap(tc, "arg")
			if analyzer.NodeKind(arg) != "ColumnRef" {
				return true
			}
			loc := analyzer.AsInt(tc, "location")
			colName := extractColumnName(arg)
			out = append(out, analyzer.Finding{
				Severity: analyzer.SeverityWarn,
				Message:  "Cast on column `" + colName + "` in WHERE prevents index usage on that column",
				Explanation: "The B-tree index is sorted on the raw column value. `col::type = X` forces the planner to " +
					"compute the cast for every row before comparing, which is not a btree-searchable predicate. " +
					"See src/backend/optimizer/path/indxpath.c (match_index_to_operand).",
				Suggestion: "Rewrite as a range predicate without casts (e.g. `col >= '2026-01-01' AND col < '2026-01-02'`) " +
					"or create an expression index: `CREATE INDEX ON t ((col::type))`.",
				Evidence: "src/backend/optimizer/path/indxpath.c; GUIA_POSTGRES_ES_2.md §35.1",
				Location: analyzer.Range{Start: loc, End: loc + 1},
			})
			return true
		})
	})
	return out
}

func init() { Register(castInWhere{}) }

// walkWhereClauses invokes visit(where) for each whereClause in the AST.
func walkWhereClauses(ast analyzer.ASTNode, visit func(analyzer.ASTNode)) {
	analyzer.Walk(ast, func(path []string, n analyzer.ASTNode) bool {
		inner := analyzer.Inner(n)
		if inner == nil {
			return true
		}
		if w := analyzer.AsMap(inner, "whereClause"); w != nil {
			visit(w)
		}
		return true
	})
}

// extractColumnName returns a dotted name from a ColumnRef fields array.
func extractColumnName(n analyzer.ASTNode) string {
	cr := analyzer.Inner(n)
	fields := analyzer.AsList(cr, "fields")
	var parts []string
	for _, f := range fields {
		fm, _ := f.(analyzer.ASTNode)
		if analyzer.NodeKind(fm) == "String" {
			parts = append(parts, analyzer.AsString(analyzer.Inner(fm), "sval"))
		}
	}
	return join(parts, ".")
}

func join(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}
