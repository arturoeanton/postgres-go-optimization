package rules

import (
	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// selectForUpdateNoLimit warns when SELECT ... FOR UPDATE / FOR NO KEY
// UPDATE is used without a LIMIT clause. Locking a potentially large set
// of rows is usually a mistake — it blocks writers, retains MultiXact
// state, and can cascade into lock storms.
type selectForUpdateNoLimit struct{}

func (selectForUpdateNoLimit) ID() string                        { return "select_for_update_no_limit" }
func (selectForUpdateNoLimit) Description() string               { return "SELECT FOR UPDATE without LIMIT can lock arbitrarily many rows" }
func (selectForUpdateNoLimit) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (selectForUpdateNoLimit) RequiresSchema() bool              { return false }
func (selectForUpdateNoLimit) RequiresExplain() bool             { return false }

func (selectForUpdateNoLimit) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	analyzer.Walk(ctx.AST, func(path []string, n analyzer.ASTNode) bool {
		if analyzer.NodeKind(n) != "SelectStmt" {
			return true
		}
		sel := analyzer.Inner(n)
		locking := analyzer.AsList(sel, "lockingClause")
		if len(locking) == 0 {
			return true
		}
		if analyzer.AsMap(sel, "limitCount") != nil {
			return true
		}
		loc := 0
		if lc := analyzer.AsMap(sel, "lockingClause"); lc != nil {
			loc = analyzer.AsInt(lc, "location")
		}
		out = append(out, analyzer.Finding{
			Severity: analyzer.SeverityWarn,
			Message:  "SELECT ... FOR UPDATE without LIMIT: may lock arbitrarily many rows",
			Explanation: "Row-level locks are stored in t_xmax of each locked tuple (infomask flag HEAP_XMAX_LOCK_ONLY; " +
				"see src/include/access/htup_details.h:197). Locking millions of rows blocks writers of those rows, " +
				"can trigger MultiXact creation if shared locks stack up, and holds back autovacuum's xmin horizon.",
			Suggestion: "Add an explicit LIMIT, or scope the SELECT to the exact rows you need, or use " +
				"FOR UPDATE SKIP LOCKED to consume a queue without blocking.",
			Evidence: "src/include/access/htup_details.h:197 (HEAP_XMAX_LOCK_ONLY); GUIA_POSTGRES_ES_2.md §13.2",
			Location: analyzer.Range{Start: loc, End: loc + 1},
		})
		return true
	})
	return out
}

func init() { Register(selectForUpdateNoLimit{}) }
