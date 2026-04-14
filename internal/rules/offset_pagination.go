package rules

import (
	"fmt"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// offsetPagination warns when a SELECT uses OFFSET N with N above a
// threshold, suggesting keyset pagination instead.
//
// OFFSET N forces the executor to produce and discard N rows (see the
// Volcano pull model in src/backend/executor/execProcnode.c). Cost is
// linear in N. Keyset pagination (`WHERE id > :last ORDER BY id LIMIT k`)
// seeks directly in the index in O(log n).
type offsetPagination struct{}

const offsetWarnThreshold = 1000

func (offsetPagination) ID() string                        { return "offset_pagination" }
func (offsetPagination) Description() string               { return "OFFSET N grows linearly in N; prefer keyset pagination" }
func (offsetPagination) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (offsetPagination) RequiresSchema() bool              { return false }
func (offsetPagination) RequiresExplain() bool             { return false }

func (offsetPagination) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	analyzer.Walk(ctx.AST, func(path []string, n analyzer.ASTNode) bool {
		if analyzer.NodeKind(n) != "SelectStmt" {
			return true
		}
		sel := analyzer.Inner(n)
		off := analyzer.AsMap(sel, "limitOffset")
		if off == nil {
			return true
		}
		// Look for A_Const -> ival
		k, v := extractIntConst(off)
		if !k {
			return true
		}
		if v < offsetWarnThreshold {
			return true
		}
		loc := findLocation(off)
		out = append(out, analyzer.Finding{
			Severity: analyzer.SeverityWarn,
			Message:  fmt.Sprintf("OFFSET %d: cost is linear in the offset (executor produces and discards %d rows)", v, v),
			Explanation: "PostgreSQL's executor uses the Volcano pull model (src/include/executor/executor.h: ExecProcNode). " +
				"OFFSET N literally calls the child node N times and throws the results away. " +
				"Keyset pagination (WHERE key > :last_seen ORDER BY key LIMIT N) seeks directly in the index in O(log n).",
			Suggestion: "Rewrite as: WHERE <order_col> > :last_value ORDER BY <order_col> LIMIT N. " +
				"Track the last value seen on the client side between pages.",
			Evidence: "src/backend/executor/nodeLimit.c (OFFSET handling); GUIA_POSTGRES_ES_2.md §35.2",
			Location: analyzer.Range{Start: loc, End: loc + 1},
		})
		return true
	})
	return out
}

func extractIntConst(n analyzer.ASTNode) (ok bool, val int) {
	// Walk looking for A_Const.ival.ival.
	var found bool
	analyzer.Walk(n, func(path []string, x analyzer.ASTNode) bool {
		if found {
			return false
		}
		if analyzer.NodeKind(x) != "A_Const" {
			return true
		}
		inner := analyzer.Inner(x)
		if iv := analyzer.AsMap(inner, "ival"); iv != nil {
			val = analyzer.AsInt(iv, "ival")
			ok = true
			found = true
			return false
		}
		return true
	})
	return
}

func findLocation(n analyzer.ASTNode) int {
	var loc int
	var found bool
	analyzer.Walk(n, func(path []string, x analyzer.ASTNode) bool {
		if found {
			return false
		}
		if v, ok := x["location"]; ok {
			switch t := v.(type) {
			case float64:
				loc = int(t)
				found = true
				return false
			}
		}
		return true
	})
	return loc
}

func init() { Register(offsetPagination{}) }
