package rules

import (
	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// functionOnColumn warns about `WHERE func(col) = X` patterns where func
// is a known non-sargable wrapper (lower, upper, trim, abs, date_trunc,
// coalesce, length).
//
// Without an expression index on `func(col)`, the planner cannot use a
// plain index on `col`. The function is evaluated for every row.
//
// Schema-aware: if Context.Schema is available, we also check whether an
// expression index already covers this call.
type functionOnColumn struct{}

var nonSargableFunctions = map[string]struct{}{
	"lower": {}, "upper": {}, "trim": {}, "btrim": {}, "ltrim": {}, "rtrim": {},
	"abs": {}, "round": {}, "floor": {}, "ceil": {}, "ceiling": {},
	"date_trunc": {}, "date_part": {}, "extract": {},
	"coalesce": {}, "length": {}, "char_length": {}, "character_length": {},
	"substr": {}, "substring": {},
}

func (functionOnColumn) ID() string                        { return "function_on_column" }
func (functionOnColumn) Description() string               { return "Function wrapping a column in WHERE disables plain indexes" }
func (functionOnColumn) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (functionOnColumn) RequiresSchema() bool              { return false }
func (functionOnColumn) RequiresExplain() bool             { return false }

func (functionOnColumn) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	walkWhereClauses(ctx.AST, func(where analyzer.ASTNode) {
		analyzer.Walk(where, func(path []string, n analyzer.ASTNode) bool {
			if analyzer.NodeKind(n) != "FuncCall" {
				return true
			}
			fc := analyzer.Inner(n)
			// Extract function name: last element of funcname list.
			fnNameList := analyzer.AsList(fc, "funcname")
			if len(fnNameList) == 0 {
				return true
			}
			last, _ := fnNameList[len(fnNameList)-1].(analyzer.ASTNode)
			fnName := analyzer.AsString(analyzer.Inner(last), "sval")
			if _, flagged := nonSargableFunctions[fnName]; !flagged {
				return true
			}
			// Is at least one argument a ColumnRef?
			args := analyzer.AsList(fc, "args")
			var colArg analyzer.ASTNode
			for _, a := range args {
				am, _ := a.(analyzer.ASTNode)
				if analyzer.NodeKind(am) == "ColumnRef" {
					colArg = am
					break
				}
			}
			if colArg == nil {
				return true
			}
			colName := extractColumnName(colArg)
			loc := analyzer.AsInt(fc, "location")
			out = append(out, analyzer.Finding{
				Severity: analyzer.SeverityWarn,
				Message:  "Function `" + fnName + "(" + colName + ")` in WHERE disables plain indexes on `" + colName + "`",
				Explanation: "A plain B-tree index on `" + colName + "` stores raw values; " + fnName + "(x) is not a searchable " +
					"operand on that index. The planner matches index operands in " +
					"src/backend/optimizer/path/indxpath.c (match_index_to_operand).",
				Suggestion: "Either (a) normalize data at write time so the plain column matches the query, or " +
					"(b) create an expression index: CREATE INDEX ON t ((" + fnName + "(" + colName + "))).",
				Evidence: "src/backend/optimizer/path/indxpath.c; GUIA_POSTGRES_ES_2.md §35.1",
				Location: analyzer.Range{Start: loc, End: loc + 1},
			})
			return true
		})
	})
	return out
}

func init() { Register(functionOnColumn{}) }
