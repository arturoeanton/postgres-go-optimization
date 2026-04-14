package rules

import (
	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// distinctOnJoins flags `SELECT DISTINCT` that likely exists to deduplicate
// a many-to-many join result. These are often better written as EXISTS
// semijoins (cheaper, clearer intent) or by fixing join predicates.
type distinctOnJoins struct{}

func (distinctOnJoins) ID() string                        { return "distinct_on_joins" }
func (distinctOnJoins) Description() string               { return "SELECT DISTINCT with joins often hides a cardinality bug; consider EXISTS" }
func (distinctOnJoins) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (distinctOnJoins) RequiresSchema() bool              { return false }
func (distinctOnJoins) RequiresExplain() bool             { return false }

func (distinctOnJoins) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	analyzer.Walk(ctx.AST, func(path []string, n analyzer.ASTNode) bool {
		if analyzer.NodeKind(n) != "SelectStmt" {
			return true
		}
		sel := analyzer.Inner(n)
		// DISTINCT is a (possibly empty) list for DISTINCT / DISTINCT ON.
		distinct := analyzer.AsList(sel, "distinctClause")
		if distinct == nil {
			return true
		}
		from := analyzer.AsList(sel, "fromClause")
		hasJoin := false
		for _, f := range from {
			fm, _ := f.(analyzer.ASTNode)
			if analyzer.NodeKind(fm) == "JoinExpr" {
				hasJoin = true
				break
			}
		}
		if !hasJoin {
			return true
		}
		out = append(out, analyzer.Finding{
			Severity: analyzer.SeverityInfo,
			Message:  "SELECT DISTINCT together with a JOIN: the DISTINCT often hides a many-to-many cardinality issue",
			Explanation: "A 1:N or N:M join naturally duplicates outer rows. DISTINCT removes the duplicates but at cost of " +
				"a Sort or HashAggregate (see src/backend/executor/nodeAgg.c AGG_HASHED). If the intent is " +
				"'does at least one match exist', EXISTS is cheaper and clearer.",
			Suggestion: "Reconsider: if you want 'rows from A where at least one match exists in B', use " +
				"WHERE EXISTS (SELECT 1 FROM B WHERE ...). DISTINCT is correct only when you actually need the B-columns.",
			Evidence: "src/backend/executor/nodeAgg.c; GUIA_POSTGRES_ES_2.md §24",
		})
		return true
	})
	return out
}

func init() { Register(distinctOnJoins{}) }
