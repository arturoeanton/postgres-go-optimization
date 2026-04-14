package rules

import (
	"strings"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// Additional AST-only rules, batch 2.

// ──────────────────────────────────────────────────────────────
// sum_case_when_count_filter — `SUM(CASE WHEN x THEN 1 ELSE 0 END)`
// is cleaner and no slower as `COUNT(*) FILTER (WHERE x)`.
// ──────────────────────────────────────────────────────────────

type sumCaseFilter struct{}

func (sumCaseFilter) ID() string                        { return "sum_case_when_count_filter" }
func (sumCaseFilter) Description() string               { return "SUM(CASE WHEN x THEN 1 ELSE 0 END) → COUNT(*) FILTER (WHERE x) is clearer" }
func (sumCaseFilter) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (sumCaseFilter) RequiresSchema() bool              { return false }
func (sumCaseFilter) RequiresExplain() bool             { return false }

func (sumCaseFilter) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	analyzer.Walk(ctx.AST, func(_ []string, n analyzer.ASTNode) bool {
		if analyzer.NodeKind(n) != "FuncCall" {
			return true
		}
		fc := analyzer.Inner(n)
		names := analyzer.AsList(fc, "funcname")
		if len(names) == 0 {
			return true
		}
		last, _ := names[len(names)-1].(analyzer.ASTNode)
		if analyzer.AsString(analyzer.Inner(last), "sval") != "sum" {
			return true
		}
		// Arg is a CaseExpr whose result is a constant int (1 vs 0)?
		args := analyzer.AsList(fc, "args")
		if len(args) != 1 {
			return true
		}
		arg, _ := args[0].(analyzer.ASTNode)
		if analyzer.NodeKind(arg) != "CaseExpr" {
			return true
		}
		loc := analyzer.AsInt(fc, "location")
		out = append(out, analyzer.Finding{
			Severity:    analyzer.SeverityInfo,
			Message:     "SUM(CASE WHEN …) pattern: consider COUNT(*) FILTER (WHERE …)",
			Explanation: "PostgreSQL supports a FILTER clause on aggregates: `COUNT(*) FILTER (WHERE cond)` counts matching rows without the CASE/ELSE dance. Equivalent performance but far more readable, and it composes better with other aggregates.",
			Suggestion:  "Replace `SUM(CASE WHEN c THEN 1 ELSE 0 END)` with `COUNT(*) FILTER (WHERE c)`. For `SUM(CASE WHEN c THEN x END)` use `SUM(x) FILTER (WHERE c)`.",
			Evidence:    "SQL:2003 FILTER clause; src/backend/executor/nodeAgg.c",
			Location:    analyzer.Range{Start: loc, End: loc + 1},
		})
		return true
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// having_without_groupby — `HAVING` is unusual without `GROUP BY`.
// ──────────────────────────────────────────────────────────────

type havingWithoutGroupBy struct{}

func (havingWithoutGroupBy) ID() string                        { return "having_without_group_by" }
func (havingWithoutGroupBy) Description() string               { return "HAVING without GROUP BY treats the whole result as one group" }
func (havingWithoutGroupBy) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (havingWithoutGroupBy) RequiresSchema() bool              { return false }
func (havingWithoutGroupBy) RequiresExplain() bool             { return false }

func (havingWithoutGroupBy) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	forEachSelect(ctx.AST, func(sel analyzer.ASTNode) {
		if analyzer.AsMap(sel, "havingClause") == nil {
			return
		}
		if len(analyzer.AsList(sel, "groupClause")) > 0 {
			return
		}
		loc := firstLocation(analyzer.AsMap(sel, "havingClause"))
		out = append(out, analyzer.Finding{
			Severity:    analyzer.SeverityInfo,
			Message:     "HAVING without GROUP BY: the whole result is treated as a single implicit group",
			Explanation: "SQL allows HAVING without GROUP BY, in which case the entire query is one group. The query returns at most one row. This is legal but usually a mistake — most readers expect GROUP BY with HAVING.",
			Suggestion:  "Either add `GROUP BY` to scope the aggregation, or move the predicate to `WHERE` if no aggregation is actually needed.",
			Evidence:    "src/backend/parser/analyze.c (transformHavingClause)",
			Location:    analyzer.Range{Start: loc, End: loc + 1},
		})
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// is_null_on_indexed_column — `WHERE col IS NULL` note.
// Informational: btree indexes DO index NULLs (unless partial), but the
// rule reminds that a partial index `WHERE col IS NULL` can be much
// smaller and faster if the null subset is small.
// ──────────────────────────────────────────────────────────────

type isNullInWhere struct{}

func (isNullInWhere) ID() string                        { return "is_null_in_where" }
func (isNullInWhere) Description() string               { return "WHERE col IS NULL: consider a partial index if NULLs are rare" }
func (isNullInWhere) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (isNullInWhere) RequiresSchema() bool              { return false }
func (isNullInWhere) RequiresExplain() bool             { return false }

func (isNullInWhere) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	walkWhereClauses(ctx.AST, func(where analyzer.ASTNode) {
		// The partial-index suggestion only makes sense when IS NULL is
		// the *driving* predicate — i.e., the sole top-level WHERE
		// condition. In multi-predicate WHERE chains the IS NULL is
		// usually a secondary correctness check, not a hot query path,
		// and a partial index is the wrong tool.
		if analyzer.NodeKind(where) != "NullTest" {
			return
		}
		nt := analyzer.Inner(where)
		if analyzer.AsString(nt, "nulltesttype") != "IS_NULL" {
			return
		}
		arg := analyzer.AsMap(nt, "arg")
		if !exprIsColumnRef(arg) {
			return
		}
		loc := analyzer.AsInt(nt, "location")
		out = append(out, analyzer.Finding{
			Severity:    analyzer.SeverityInfo,
			Message:     "`IS NULL` predicate: for rare-NULL columns on big tables, consider a partial index",
			Explanation: "A btree index includes NULLs by default and can serve `IS NULL` searches, but it stores them mixed with all the non-null values. A partial index `CREATE INDEX ... WHERE col IS NULL` is a fraction of the size when most rows have non-null col.",
			Suggestion:  "If `col IS NULL` is a hot query path: `CREATE INDEX idx ON t (id) WHERE col IS NULL`. Otherwise this is just a heads-up.",
			Evidence:    "src/backend/access/nbtree/README (NULL handling)",
			Location:    analyzer.Range{Start: loc, End: loc + 1},
		})
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// cte_unused — WITH clause declared but never referenced downstream.
// ──────────────────────────────────────────────────────────────

type cteUnused struct{}

func (cteUnused) ID() string                        { return "cte_unused" }
func (cteUnused) Description() string               { return "CTE declared but not referenced" }
func (cteUnused) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (cteUnused) RequiresSchema() bool              { return false }
func (cteUnused) RequiresExplain() bool             { return false }

func (cteUnused) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	// withClause is emitted inline on SelectStmt (unwrapped), and also
	// appears as a wrapped {"WithClause": {...}} node inside sublinks.
	// Accept either shape.
	analyzer.Walk(ctx.AST, func(_ []string, n analyzer.ASTNode) bool {
		wc := n
		if analyzer.NodeKind(n) == "WithClause" {
			wc = analyzer.Inner(n)
		}
		if _, ok := wc["ctes"]; !ok {
			return true
		}
		// Collect CTE names from ctes[*].CommonTableExpr.ctename
		var names []string
		var locs = map[string]int{}
		for _, c := range analyzer.AsList(wc, "ctes") {
			cm, _ := c.(analyzer.ASTNode)
			cte := analyzer.Inner(cm)
			name := analyzer.AsString(cte, "ctename")
			if name != "" {
				names = append(names, name)
				locs[name] = analyzer.AsInt(cte, "location")
			}
		}
		if len(names) == 0 {
			return true
		}
		// For each name, count RangeVar references with relname == name OUTSIDE the WITH itself.
		// Heuristic: count occurrences in the entire AST and subtract 1 (the declaration site).
		refCount := map[string]int{}
		analyzer.Walk(ctx.AST, func(_ []string, x analyzer.ASTNode) bool {
			if analyzer.NodeKind(x) == "RangeVar" {
				relname := analyzer.AsString(analyzer.Inner(x), "relname")
				for _, n := range names {
					if strings.EqualFold(n, relname) {
						refCount[n]++
					}
				}
			}
			return true
		})
		for _, n := range names {
			if refCount[n] > 0 {
				continue
			}
			out = append(out, analyzer.Finding{
				Severity:    analyzer.SeverityInfo,
				Message:     "CTE `" + n + "` is declared but never referenced",
				Explanation: "A CTE in a WITH clause that nothing references is dead code. If it has side effects (INSERT/UPDATE/DELETE RETURNING), it's still evaluated as a data-modifying CTE; otherwise it is elided by the planner.",
				Suggestion:  "Either remove the unused CTE or add the reference it is supposed to feed.",
				Evidence:    "src/backend/optimizer/plan/subselect.c",
				Location:    analyzer.Range{Start: locs[n], End: locs[n] + 1},
			})
		}
		return true
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// vacuum_full_in_script — running VACUUM FULL from application code.
// ──────────────────────────────────────────────────────────────

type vacuumFullScript struct{}

func (vacuumFullScript) ID() string                        { return "vacuum_full_in_script" }
func (vacuumFullScript) Description() string               { return "VACUUM FULL rewrites the entire table under ACCESS EXCLUSIVE — risky in app code" }
func (vacuumFullScript) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (vacuumFullScript) RequiresSchema() bool              { return false }
func (vacuumFullScript) RequiresExplain() bool             { return false }

func (vacuumFullScript) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	forEachStmt(ctx.AST, func(kind string, inner, wrap analyzer.ASTNode) {
		if kind != "VacuumStmt" {
			return
		}
		isFull := false
		for _, opt := range analyzer.AsList(inner, "options") {
			om, _ := opt.(analyzer.ASTNode)
			// DefElem defname == "full"
			if analyzer.NodeKind(om) == "DefElem" &&
				strings.EqualFold(analyzer.AsString(analyzer.Inner(om), "defname"), "full") {
				isFull = true
				break
			}
		}
		// Older parser tree: `VacuumStmt.is_full`
		if !isFull {
			if v, ok := inner["is_full"]; ok {
				if b, ok := v.(bool); ok && b {
					isFull = true
				}
			}
		}
		if !isFull {
			return
		}
		out = append(out, analyzer.Finding{
			Severity:    analyzer.SeverityWarn,
			Message:     "VACUUM FULL rewrites the whole table under ACCESS EXCLUSIVE",
			Explanation: "VACUUM FULL is not an incremental vacuum: it copies every visible tuple into a new relfilenode and swaps it in, holding ACCESS EXCLUSIVE on the table for the entire duration. For busy tables this blocks every reader and writer until it finishes.",
			Suggestion:  "For regular maintenance use plain VACUUM (autovacuum is fine). If bloat is real, use pg_repack or pg_squeeze to rewrite concurrently without taking AccessExclusive.",
			Evidence:    "src/backend/commands/vacuum.c",
			Location:    analyzer.Range{Start: firstLocation(inner), End: firstLocation(inner) + 1},
		})
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// in_subquery_readability — `WHERE x IN (SELECT …)` → EXISTS.
// Informational: semantically equivalent in most cases, but EXISTS is
// more explicit about intent and is the safer default with NULLs.
// ──────────────────────────────────────────────────────────────

type inSubqueryReadability struct{}

func (inSubqueryReadability) ID() string                        { return "in_subquery_readability" }
func (inSubqueryReadability) Description() string               { return "`IN (subquery)` often clearer as EXISTS" }
func (inSubqueryReadability) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (inSubqueryReadability) RequiresSchema() bool              { return false }
func (inSubqueryReadability) RequiresExplain() bool             { return false }

func (inSubqueryReadability) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	walkWhereClauses(ctx.AST, func(where analyzer.ASTNode) {
		analyzer.Walk(where, func(_ []string, n analyzer.ASTNode) bool {
			if analyzer.NodeKind(n) != "SubLink" {
				return true
			}
			sl := analyzer.Inner(n)
			if analyzer.AsString(sl, "subLinkType") != "ANY_SUBLINK" {
				return true
			}
			// ANY_SUBLINK with '=' operator = IN
			ops := analyzer.AsList(sl, "operName")
			isEq := false
			for _, o := range ops {
				if mm, ok := o.(analyzer.ASTNode); ok {
					if analyzer.AsString(analyzer.Inner(mm), "sval") == "=" {
						isEq = true
					}
				}
			}
			if !isEq && len(ops) != 0 {
				return true
			}
			loc := analyzer.AsInt(sl, "location")
			out = append(out, analyzer.Finding{
				Severity:    analyzer.SeverityInfo,
				Message:     "`IN (SELECT …)` is equivalent to `EXISTS (SELECT 1 FROM … WHERE col = outer.col)`",
				Explanation: "The planner converts IN-subqueries to semijoins internally, so performance is usually equal. EXISTS is more explicit about the fact that we only care about existence — many teams prefer it stylistically, and it also avoids the NULL semantics issue that NOT IN has.",
				Suggestion:  "Consider rewriting as EXISTS for readability, especially if you have a matching NOT EXISTS variant nearby.",
				Evidence:    "src/backend/optimizer/plan/subselect.c (pull_up_sublinks)",
				Location:    analyzer.Range{Start: loc, End: loc + 1},
			})
			return true
		})
	})
	return out
}

func init() {
	Register(sumCaseFilter{})
	Register(havingWithoutGroupBy{})
	Register(isNullInWhere{})
	Register(cteUnused{})
	Register(vacuumFullScript{})
	Register(inSubqueryReadability{})
}
