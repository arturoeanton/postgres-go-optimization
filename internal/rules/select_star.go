package rules

import (
	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// selectStar warns about `SELECT *` in top-level queries.
//
// Why it matters (see GUIA_POSTGRES_ES_2.md §35 and discussion around
// Index-Only Scans in §22.3): `*` defeats Index-Only Scan because the
// selected columns must all be covered by the index (including INCLUDE);
// it also triggers TOAST detoast for large jsonb/text columns, and wastes
// network bandwidth by sending everything.
//
// We DO NOT warn when `*` appears inside EXISTS / NOT EXISTS — there the
// target list is discarded by the planner and `*` is idiomatic.
type selectStar struct{}

func (selectStar) ID() string                        { return "select_star" }
func (selectStar) Description() string               { return "SELECT * selects all columns, which defeats Index-Only Scans and triggers TOAST detoast when unneeded" }
func (selectStar) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (selectStar) RequiresSchema() bool              { return false }
func (selectStar) RequiresExplain() bool             { return false }

func (selectStar) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	analyzer.Walk(ctx.AST, func(path []string, n analyzer.ASTNode) bool {
		// Skip subqueries inside EXISTS: SubLink with type EXISTS_SUBLINK.
		if inner := analyzer.Inner(n); inner != nil && analyzer.NodeKind(n) == "SubLink" {
			if t := analyzer.AsString(inner, "subLinkType"); t == "EXISTS_SUBLINK" {
				return false // do not descend
			}
		}
		// Look for ColumnRef whose fields contain A_Star.
		if analyzer.NodeKind(n) != "ColumnRef" {
			return true
		}
		inner := analyzer.Inner(n)
		fields := analyzer.AsList(inner, "fields")
		if len(fields) == 0 {
			return true
		}
		last, _ := fields[len(fields)-1].(analyzer.ASTNode)
		if analyzer.NodeKind(last) != "A_Star" {
			return true
		}
		loc := analyzer.AsInt(inner, "location")
		out = append(out, analyzer.Finding{
			Severity: analyzer.SeverityWarn,
			Message:  "SELECT * pulls every column, defeating Index-Only Scans and forcing TOAST detoast for large columns",
			Explanation: "Expansion happens during parse_analyze (src/backend/parser/parse_target.c: ExpandColumnRefStar, ExpandAllTables). " +
				"The expanded target list then requires each column to be projected at runtime. If an index covers the used columns " +
				"(with INCLUDE), an explicit column list can enable Index-Only Scan (see src/backend/executor/nodeIndexonlyscan.c).",
			Suggestion: "List only the columns you actually need. For EXISTS subqueries, SELECT * is fine — the target list is discarded.",
			Evidence:   "src/backend/parser/parse_target.c:ExpandColumnRefStar",
			Location:   analyzer.Range{Start: loc, End: loc + 1},
		})
		return true
	})
	return out
}

func init() { Register(selectStar{}) }
