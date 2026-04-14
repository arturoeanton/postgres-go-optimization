package rules

import (
	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// missingWhere is a hard error: UPDATE or DELETE without WHERE affects
// every row in the table. Almost always a bug. If it's intentional, the
// user can either keep the statement and ignore this rule (or bypass
// via `--rules all,-missing_where`) or use `WHERE true` to make intent
// explicit.
type missingWhere struct{}

func (missingWhere) ID() string                        { return "missing_where" }
func (missingWhere) Description() string               { return "UPDATE/DELETE without WHERE touches every row" }
func (missingWhere) DefaultSeverity() analyzer.Severity { return analyzer.SeverityError }
func (missingWhere) RequiresSchema() bool              { return false }
func (missingWhere) RequiresExplain() bool             { return false }

func (missingWhere) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	stmts := analyzer.AsList(ctx.AST, "stmts")
	for _, s := range stmts {
		sm, _ := s.(analyzer.ASTNode)
		stmt := analyzer.AsMap(sm, "stmt")
		kind := analyzer.NodeKind(stmt)
		if kind != "UpdateStmt" && kind != "DeleteStmt" {
			continue
		}
		inner := analyzer.Inner(stmt)
		if w := analyzer.AsMap(inner, "whereClause"); w != nil {
			continue
		}
		// Find the relation being modified.
		relTarget := "table"
		if r := analyzer.AsMap(inner, "relation"); r != nil {
			if name := analyzer.AsString(r, "relname"); name != "" {
				relTarget = name
			}
		}
		action := "UPDATE"
		if kind == "DeleteStmt" {
			action = "DELETE"
		}
		out = append(out, analyzer.Finding{
			Severity: analyzer.SeverityError,
			Message:  action + " without WHERE affects every row of `" + relTarget + "`",
			Explanation: "Without a WHERE, the executor applies the modification to every tuple visible in the " +
				"relation scan (src/backend/executor/nodeModifyTable.c). For large tables this is a high-impact " +
				"operation: massive WAL, long-running transaction, autovacuum suppression.",
			Suggestion: "Add a WHERE clause that limits the affected rows. If a full-table change is intended, make " +
				"it explicit with `WHERE true` and consider batching to avoid one gigantic transaction.",
			Evidence: "src/backend/executor/nodeModifyTable.c; GUIA_POSTGRES_ES_2.md §35.3",
		})
	}
	return out
}

func init() { Register(missingWhere{}) }
