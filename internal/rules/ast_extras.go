// Package rules — additional AST-only rules.
//
// One file groups several rules to keep the repository browsable. Each
// rule is still a distinct struct with its own ID; tests target them
// individually via rules_test.go.
//
// Pattern recap: struct with no fields, five getters (ID, Description,
// DefaultSeverity, RequiresSchema, RequiresExplain), a Check method,
// and a Register call from init().
package rules

import (
	"fmt"
	"strings"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// ──────────────────────────────────────────────────────────────
// count_star_big — COUNT(*) on a large table is expensive.
// ──────────────────────────────────────────────────────────────

type countStarBig struct{}

func (countStarBig) ID() string                        { return "count_star_big" }
func (countStarBig) Description() string               { return "COUNT(*) scans the whole table; consider pg_class.reltuples for estimates" }
func (countStarBig) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (countStarBig) RequiresSchema() bool              { return false }
func (countStarBig) RequiresExplain() bool             { return false }

func (countStarBig) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	analyzer.Walk(ctx.AST, func(_ []string, n analyzer.ASTNode) bool {
		if analyzer.NodeKind(n) != "FuncCall" {
			return true
		}
		fc := analyzer.Inner(n)
		names := analyzer.AsList(fc, "funcname")
		if len(names) != 1 {
			return true
		}
		last, _ := names[0].(analyzer.ASTNode)
		if analyzer.AsString(analyzer.Inner(last), "sval") != "count" {
			return true
		}
		if !isStarArgs(fc) {
			return true
		}
		loc := analyzer.AsInt(fc, "location")
		out = append(out, analyzer.Finding{
			Severity: analyzer.SeverityInfo,
			Message:  "COUNT(*) scans the whole relation; for big tables consider pg_class.reltuples for a fast estimate",
			Explanation: "COUNT(*) executes a full scan (src/backend/executor/nodeAgg.c). For tables in the millions of " +
				"rows this reads every heap page. If an exact count isn't required, `SELECT reltuples::bigint " +
				"FROM pg_class WHERE oid = 'tbl'::regclass` gives the ANALYZE estimate in O(1).",
			Suggestion: "Use pg_class.reltuples for estimates; keep COUNT(*) for audits or small sets. " +
				"A partial materialized view with a periodic refresh is another option.",
			Evidence: "src/backend/executor/nodeAgg.c; GUIA_POSTGRES_ES_2.md §35.8",
			Location: analyzer.Range{Start: loc, End: loc + 1},
		})
		return true
	})
	return out
}

// isStarArgs returns true when a FuncCall has `agg_star == true`.
// (pg_query represents count(*) with an empty args list and agg_star=true.)
func isStarArgs(fc analyzer.ASTNode) bool {
	if v, ok := fc["agg_star"]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// ──────────────────────────────────────────────────────────────
// order_by_ordinal / group_by_ordinal — positional refs are fragile.
// ──────────────────────────────────────────────────────────────

type orderByOrdinal struct{}

func (orderByOrdinal) ID() string                        { return "order_by_ordinal" }
func (orderByOrdinal) Description() string               { return "ORDER BY 1, 2 is fragile; use explicit column names" }
func (orderByOrdinal) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (orderByOrdinal) RequiresSchema() bool              { return false }
func (orderByOrdinal) RequiresExplain() bool             { return false }

func (orderByOrdinal) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	forEachSelect(ctx.AST, func(sel analyzer.ASTNode) {
		for _, s := range analyzer.AsList(sel, "sortClause") {
			sb, _ := s.(analyzer.ASTNode)
			if analyzer.NodeKind(sb) != "SortBy" {
				continue
			}
			node := analyzer.AsMap(analyzer.Inner(sb), "node")
			if analyzer.NodeKind(node) != "A_Const" {
				continue
			}
			if iv := analyzer.AsMap(analyzer.Inner(node), "ival"); iv != nil {
				loc := analyzer.AsInt(analyzer.Inner(node), "location")
				out = append(out, analyzer.Finding{
					Severity:   analyzer.SeverityInfo,
					Message:    "ORDER BY uses a positional reference; prefer explicit column names",
					Explanation: "Positional ORDER BY is legal SQL but fragile: renaming a column or reordering the SELECT list silently changes the sort. Explicit names are also easier to grep for.",
					Suggestion: "Replace `ORDER BY 1, 2` with the real column/alias names.",
					Evidence:   "SQL standard behavior; see src/backend/parser/parse_clause.c",
					Location:   analyzer.Range{Start: loc, End: loc + 1},
				})
			}
		}
	})
	return out
}

type groupByOrdinal struct{}

func (groupByOrdinal) ID() string                        { return "group_by_ordinal" }
func (groupByOrdinal) Description() string               { return "GROUP BY 1 is fragile; use explicit column names" }
func (groupByOrdinal) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (groupByOrdinal) RequiresSchema() bool              { return false }
func (groupByOrdinal) RequiresExplain() bool             { return false }

func (groupByOrdinal) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	forEachSelect(ctx.AST, func(sel analyzer.ASTNode) {
		for _, g := range analyzer.AsList(sel, "groupClause") {
			gm, _ := g.(analyzer.ASTNode)
			if analyzer.NodeKind(gm) != "A_Const" {
				continue
			}
			if iv := analyzer.AsMap(analyzer.Inner(gm), "ival"); iv != nil {
				loc := analyzer.AsInt(analyzer.Inner(gm), "location")
				out = append(out, analyzer.Finding{
					Severity:    analyzer.SeverityInfo,
					Message:     "GROUP BY uses a positional reference; prefer explicit column names",
					Explanation: "Same caveat as ORDER BY: positional GROUP BY silently changes when the SELECT list changes.",
					Suggestion:  "Replace `GROUP BY 1[, 2 …]` with explicit column names or expressions.",
					Evidence:    "src/backend/parser/parse_clause.c",
					Location:    analyzer.Range{Start: loc, End: loc + 1},
				})
			}
		}
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// implicit_cross_join — FROM a, b without a join condition.
// ──────────────────────────────────────────────────────────────

type implicitCrossJoin struct{}

func (implicitCrossJoin) ID() string                        { return "implicit_cross_join" }
func (implicitCrossJoin) Description() string               { return "Multiple tables in FROM without a join predicate = Cartesian product" }
func (implicitCrossJoin) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (implicitCrossJoin) RequiresSchema() bool              { return false }
func (implicitCrossJoin) RequiresExplain() bool             { return false }

func (implicitCrossJoin) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	forEachSelect(ctx.AST, func(sel analyzer.ASTNode) {
		from := analyzer.AsList(sel, "fromClause")
		if len(from) < 2 {
			return
		}
		// All items are RangeVar (plain tables) ⇒ implicit cross join
		// unless there is a WHERE that mentions both.
		allRange := true
		for _, f := range from {
			fm, _ := f.(analyzer.ASTNode)
			if analyzer.NodeKind(fm) != "RangeVar" {
				allRange = false
				break
			}
		}
		if !allRange {
			return
		}
		// Require that WHERE references at least two columns of different qualifiers,
		// a cheap proxy for "has a join predicate".
		where := analyzer.AsMap(sel, "whereClause")
		if where != nil && hasJoinPredicate(where) {
			return
		}
		out = append(out, analyzer.Finding{
			Severity:    analyzer.SeverityInfo,
			Message:     "FROM lists multiple tables with no join condition — a Cartesian product",
			Explanation: "Without a join predicate the planner produces every combination of rows (NxM). " +
				"Almost always unintended; the resulting cardinality explodes quickly.",
			Suggestion: "Add an ON/WHERE predicate that ties the tables; or use explicit `JOIN ... ON` syntax which makes missing predicates obvious.",
			Evidence:   "src/backend/optimizer/path/joinrels.c",
			Location:   analyzer.Range{Start: firstLocation(sel), End: firstLocation(sel) + 1},
		})
	})
	return out
}

// hasJoinPredicate is a heuristic: does the WHERE contain A_Expr whose both
// sides are ColumnRefs with different qualifiers?
func hasJoinPredicate(where analyzer.ASTNode) bool {
	found := false
	analyzer.Walk(where, func(_ []string, n analyzer.ASTNode) bool {
		if found {
			return false
		}
		if analyzer.NodeKind(n) != "A_Expr" {
			return true
		}
		e := analyzer.Inner(n)
		l := analyzer.AsMap(e, "lexpr")
		r := analyzer.AsMap(e, "rexpr")
		if analyzer.NodeKind(l) == "ColumnRef" && analyzer.NodeKind(r) == "ColumnRef" {
			lq, _ := splitColumn(l)
			rq, _ := splitColumn(r)
			if lq != "" && rq != "" && lq != rq {
				found = true
			}
		}
		return true
	})
	return found
}

// ──────────────────────────────────────────────────────────────
// boolean_equals_true — `WHERE col = true` is redundant.
// ──────────────────────────────────────────────────────────────

type booleanEqualsTrue struct{}

func (booleanEqualsTrue) ID() string                        { return "boolean_equals_true" }
func (booleanEqualsTrue) Description() string               { return "`col = true` / `col = false` is redundant" }
func (booleanEqualsTrue) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (booleanEqualsTrue) RequiresSchema() bool              { return false }
func (booleanEqualsTrue) RequiresExplain() bool             { return false }

func (booleanEqualsTrue) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	walkWhereClauses(ctx.AST, func(where analyzer.ASTNode) {
		analyzer.Walk(where, func(_ []string, n analyzer.ASTNode) bool {
			if analyzer.NodeKind(n) != "A_Expr" {
				return true
			}
			e := analyzer.Inner(n)
			if extractOpName(analyzer.AsList(e, "name")) != "=" {
				return true
			}
			r := analyzer.AsMap(e, "rexpr")
			rKind := analyzer.NodeKind(r)
			if rKind != "TypeCast" && rKind != "A_Const" {
				return true
			}
			// Either a direct boolean literal (A_Const.boolval) or a
			// cast of `'true'`/`'false'` via TypeCast — read the inner
			// node where `location` actually lives.
			inner := analyzer.Inner(r)
			// Fast path: A_Const with a boolval sub-object is a boolean literal.
			if rKind == "A_Const" {
				if _, ok := inner["boolval"]; !ok {
					return true
				}
			}
			litStart := analyzer.AsInt(inner, "location")
			end := litStart + 5
			if end > len(ctx.Source) {
				end = len(ctx.Source)
			}
			lit := ""
			if litStart >= 0 && end > litStart {
				lit = strings.ToLower(ctx.Source[litStart:end])
			}
			if !strings.HasPrefix(lit, "true") && !strings.HasPrefix(lit, "false") {
				return true
			}
			loc := analyzer.AsInt(e, "location")
			out = append(out, analyzer.Finding{
				Severity:    analyzer.SeverityInfo,
				Message:     "`col = true/false` is redundant",
				Explanation: "A boolean column can be used directly. `WHERE col` is equivalent to `WHERE col = true`; `WHERE NOT col` replaces `= false`. Simpler and lets the planner reason uniformly.",
				Suggestion:  "Replace `WHERE flag = true` with `WHERE flag`; `WHERE flag = false` with `WHERE NOT flag` (watch out for NULLs with `WHERE flag IS TRUE` if you want strict true).",
				Evidence:    "SQL semantics; standard clean-up pattern",
				Location:    analyzer.Range{Start: loc, End: loc + 1},
			})
			return true
		})
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// coalesce_in_where — `WHERE coalesce(col, 0) = …` blocks indexes.
// ──────────────────────────────────────────────────────────────

type coalesceInWhere struct{}

func (coalesceInWhere) ID() string                        { return "coalesce_in_where" }
func (coalesceInWhere) Description() string               { return "COALESCE on an indexed column blocks index usage" }
func (coalesceInWhere) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (coalesceInWhere) RequiresSchema() bool              { return false }
func (coalesceInWhere) RequiresExplain() bool             { return false }

func (coalesceInWhere) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	walkWhereClauses(ctx.AST, func(where analyzer.ASTNode) {
		analyzer.Walk(where, func(_ []string, n analyzer.ASTNode) bool {
			if analyzer.NodeKind(n) != "CoalesceExpr" {
				return true
			}
			ce := analyzer.Inner(n)
			args := analyzer.AsList(ce, "args")
			if len(args) == 0 {
				return true
			}
			first, _ := args[0].(analyzer.ASTNode)
			if !exprIsColumnRef(first) {
				return true
			}
			colName := extractColumnName(first)
			loc := analyzer.AsInt(ce, "location")
			out = append(out, analyzer.Finding{
				Severity:    analyzer.SeverityWarn,
				Message:     "COALESCE(" + colName + ", …) in WHERE blocks index usage",
				Explanation: "COALESCE wraps the column in a function-like expression; a plain btree on the column cannot be used. Either handle NULL in your predicate (`col IS NULL OR col = X`) or create an expression index.",
				Suggestion:  "Rewrite: `(col = X OR col IS NULL)` ; or `CREATE INDEX ON t ((COALESCE(col, default_val)))`.",
				Evidence:    "src/backend/optimizer/path/indxpath.c",
				Location:    analyzer.Range{Start: loc, End: loc + 1},
			})
			return true
		})
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// order_by_random — `ORDER BY random() LIMIT k` does a full scan.
// ──────────────────────────────────────────────────────────────

type orderByRandom struct{}

func (orderByRandom) ID() string                        { return "order_by_random" }
func (orderByRandom) Description() string               { return "ORDER BY random() LIMIT k scans the whole relation" }
func (orderByRandom) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (orderByRandom) RequiresSchema() bool              { return false }
func (orderByRandom) RequiresExplain() bool             { return false }

func (orderByRandom) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	forEachSelect(ctx.AST, func(sel analyzer.ASTNode) {
		for _, s := range analyzer.AsList(sel, "sortClause") {
			sb, _ := s.(analyzer.ASTNode)
			node := analyzer.AsMap(analyzer.Inner(sb), "node")
			if analyzer.NodeKind(node) != "FuncCall" {
				continue
			}
			names := analyzer.AsList(analyzer.Inner(node), "funcname")
			if len(names) == 0 {
				continue
			}
			last, _ := names[len(names)-1].(analyzer.ASTNode)
			if analyzer.AsString(analyzer.Inner(last), "sval") != "random" {
				continue
			}
			loc := analyzer.AsInt(analyzer.Inner(node), "location")
			out = append(out, analyzer.Finding{
				Severity:    analyzer.SeverityWarn,
				Message:     "ORDER BY random() forces a full scan + full sort",
				Explanation: "random() is evaluated per row; the sort doesn't benefit from any index. For large tables this reads every row.",
				Suggestion:  "For sampling, use TABLESAMPLE or `OFFSET floor(random() * (SELECT count(*) FROM t)) LIMIT 1` on a table with a primary key. For multiple random rows, see `tsm_system_rows`.",
				Evidence:    "src/backend/executor/nodeSort.c; contrib/tsm_system_rows",
				Location:    analyzer.Range{Start: loc, End: loc + 1},
			})
		}
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// subquery_in_select — correlated subquery in the select list (N+1).
// ──────────────────────────────────────────────────────────────

type subqueryInSelect struct{}

func (subqueryInSelect) ID() string                        { return "subquery_in_select" }
func (subqueryInSelect) Description() string               { return "Correlated subquery in SELECT list is an N+1 pattern" }
func (subqueryInSelect) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (subqueryInSelect) RequiresSchema() bool              { return false }
func (subqueryInSelect) RequiresExplain() bool             { return false }

func (subqueryInSelect) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	forEachSelect(ctx.AST, func(sel analyzer.ASTNode) {
		for _, t := range analyzer.AsList(sel, "targetList") {
			tm, _ := t.(analyzer.ASTNode)
			rt := analyzer.Inner(tm)
			val := analyzer.AsMap(rt, "val")
			if analyzer.NodeKind(val) != "SubLink" {
				continue
			}
			sl := analyzer.Inner(val)
			kind := analyzer.AsString(sl, "subLinkType")
			if kind != "EXPR_SUBLINK" && kind != "ARRAY_SUBLINK" {
				continue
			}
			loc := analyzer.AsInt(rt, "location")
			out = append(out, analyzer.Finding{
				Severity:    analyzer.SeverityWarn,
				Message:     "Subquery in SELECT list (correlated or not) often beats the planner's batching",
				Explanation: "PostgreSQL evaluates scalar subqueries once per outer row when correlated, and only sometimes inlines uncorrelated ones. Equivalent LEFT JOIN or LATERAL is usually faster and clearer.",
				Suggestion:  "Rewrite as LEFT JOIN LATERAL (SELECT … LIMIT 1) or as a regular LEFT JOIN with aggregation on the outer level.",
				Evidence:    "src/backend/optimizer/plan/subselect.c",
				Location:    analyzer.Range{Start: loc, End: loc + 1},
			})
		}
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// window_without_partition — `OVER ()` empty window.
// ──────────────────────────────────────────────────────────────

type windowEmpty struct{}

func (windowEmpty) ID() string                        { return "window_empty" }
func (windowEmpty) Description() string               { return "OVER () with no PARTITION BY aggregates across the whole table — often unintended" }
func (windowEmpty) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (windowEmpty) RequiresSchema() bool              { return false }
func (windowEmpty) RequiresExplain() bool             { return false }

func (windowEmpty) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	// pg_query emits the window definition either as a wrapped
	// {"WindowDef": {...}} node (for top-level WINDOW clauses) or
	// inline under a FuncCall's "over" key (for inline OVER (...)).
	// Both shapes carry `frameOptions`, so we match on that.
	analyzer.Walk(ctx.AST, func(_ []string, n analyzer.ASTNode) bool {
		wd := n
		if analyzer.NodeKind(n) == "WindowDef" {
			wd = analyzer.Inner(n)
		}
		if _, ok := wd["frameOptions"]; !ok {
			return true
		}
		if len(analyzer.AsList(wd, "partitionClause")) == 0 &&
			len(analyzer.AsList(wd, "orderClause")) == 0 {
			loc := analyzer.AsInt(wd, "location")
			out = append(out, analyzer.Finding{
				Severity:    analyzer.SeverityInfo,
				Message:     "Window function uses `OVER ()` — aggregates across the full result",
				Explanation: "An empty window means every row's window frame contains every other row. This is valid but often accidental; the intent is usually `PARTITION BY something`.",
				Suggestion:  "Add `PARTITION BY …` to scope the window, or `ORDER BY … ROWS BETWEEN …` for running aggregates.",
				Evidence:    "src/backend/executor/nodeWindowAgg.c",
				Location:    analyzer.Range{Start: loc, End: loc + 1},
			})
		}
		return true
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// truncate_in_transaction — TRUNCATE inside long-running tx is risky.
// ──────────────────────────────────────────────────────────────

type truncateInTx struct{}

func (truncateInTx) ID() string                        { return "truncate_in_transaction" }
func (truncateInTx) Description() string               { return "TRUNCATE within a multi-statement transaction holds ACCESS EXCLUSIVE lock until commit" }
func (truncateInTx) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (truncateInTx) RequiresSchema() bool              { return false }
func (truncateInTx) RequiresExplain() bool             { return false }

func (truncateInTx) Check(ctx *analyzer.Context) []analyzer.Finding {
	// Detect pattern: TruncateStmt appears in a file that also contains BEGIN and more work.
	var hasBegin, hasTrunc, multiStmt bool
	var loc int
	count := 0
	forEachStmt(ctx.AST, func(kind string, inner analyzer.ASTNode, wrap analyzer.ASTNode) {
		count++
		if kind == "TransactionStmt" && analyzer.AsString(inner, "kind") == "TRANS_STMT_BEGIN" {
			hasBegin = true
		}
		if kind == "TruncateStmt" && loc == 0 {
			hasTrunc = true
			loc = firstLocation(inner)
		}
	})
	multiStmt = count > 1
	if !(hasBegin && hasTrunc && multiStmt) {
		return nil
	}
	return []analyzer.Finding{{
		Severity:    analyzer.SeverityWarn,
		Message:     "TRUNCATE inside a multi-statement transaction holds ACCESS EXCLUSIVE until commit",
		Explanation: "TRUNCATE takes ACCESS EXCLUSIVE on the target relation (src/backend/commands/tablecmds.c). Inside a transaction this lock is held through every subsequent statement, blocking every reader/writer of the table.",
		Suggestion:  "Run TRUNCATE in its own transaction; or use a partition swap / DROP+CREATE pattern on non-critical tables.",
		Evidence:    "src/backend/commands/tablecmds.c (ExecuteTruncate)",
		Location:    analyzer.Range{Start: loc, End: loc + 1},
	}}
}

// ──────────────────────────────────────────────────────────────
// interval_on_indexed_column — e.g. `WHERE ts + interval '1d' < now()`.
// ──────────────────────────────────────────────────────────────

type intervalArithmetic struct{}

func (intervalArithmetic) ID() string                        { return "interval_on_indexed_column" }
func (intervalArithmetic) Description() string               { return "Interval arithmetic on an indexed timestamp column blocks index usage" }
func (intervalArithmetic) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (intervalArithmetic) RequiresSchema() bool              { return false }
func (intervalArithmetic) RequiresExplain() bool             { return false }

func (intervalArithmetic) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	walkWhereClauses(ctx.AST, func(where analyzer.ASTNode) {
		analyzer.Walk(where, func(_ []string, n analyzer.ASTNode) bool {
			if analyzer.NodeKind(n) != "A_Expr" {
				return true
			}
			e := analyzer.Inner(n)
			if extractOpName(analyzer.AsList(e, "name")) != "+" &&
				extractOpName(analyzer.AsList(e, "name")) != "-" {
				return true
			}
			l := analyzer.AsMap(e, "lexpr")
			r := analyzer.AsMap(e, "rexpr")
			// column on one side, TypeCast to interval on the other.
			col, other := l, r
			if !exprIsColumnRef(col) {
				col, other = r, l
			}
			if !exprIsColumnRef(col) {
				return true
			}
			if analyzer.NodeKind(other) != "TypeCast" {
				return true
			}
			tn := analyzer.AsMap(analyzer.Inner(other), "typeName")
			names := analyzer.AsList(tn, "names")
			for _, nm := range names {
				if mm, ok := nm.(analyzer.ASTNode); ok {
					if analyzer.AsString(analyzer.Inner(mm), "sval") == "interval" {
						loc := analyzer.AsInt(e, "location")
						out = append(out, analyzer.Finding{
							Severity:    analyzer.SeverityWarn,
							Message:     "Interval arithmetic moves the indexed column under an expression, blocking index usage",
							Explanation: "`col + interval 'X' op Y` can be algebraically rearranged to `col op Y - interval 'X'`. The rearranged form keeps the column as the direct operand of the comparison, which the planner needs to match an index.",
							Suggestion:  "Move the interval to the other side: `ts < now() - interval '1 day'` instead of `ts + interval '1 day' < now()`.",
							Evidence:    "src/backend/optimizer/path/indxpath.c",
							Location:    analyzer.Range{Start: loc, End: loc + 1},
						})
						return false
					}
				}
			}
			return true
		})
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// recursive_cte_without_limit — recursive CTE without a LIMIT/terminator.
// ──────────────────────────────────────────────────────────────

type recursiveCTENoLimit struct{}

func (recursiveCTENoLimit) ID() string                        { return "recursive_cte_no_limit" }
func (recursiveCTENoLimit) Description() string               { return "Recursive CTE without LIMIT/visited guard — risk of infinite recursion" }
func (recursiveCTENoLimit) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (recursiveCTENoLimit) RequiresSchema() bool              { return false }
func (recursiveCTENoLimit) RequiresExplain() bool             { return false }

func (recursiveCTENoLimit) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	// withClause can appear either wrapped as {"WithClause": {...}} or
	// inline under SelectStmt. Both shapes carry the `recursive` flag
	// and a `ctes` list, which is what we need to detect either way.
	analyzer.Walk(ctx.AST, func(_ []string, n analyzer.ASTNode) bool {
		wc := n
		if analyzer.NodeKind(n) == "WithClause" {
			wc = analyzer.Inner(n)
		}
		if _, ok := wc["ctes"]; !ok {
			return true
		}
		if rec, ok := wc["recursive"]; !ok || rec != true {
			return true
		}
		// A LIMIT anywhere in the query (including on the outer SELECT
		// that consumes the CTE) acts as a termination bound, so we
		// walk the full AST rather than only the WITH subtree.
		hasLimit := false
		analyzer.Walk(ctx.AST, func(_ []string, x analyzer.ASTNode) bool {
			if hasLimit {
				return false
			}
			if analyzer.NodeKind(x) == "SelectStmt" {
				if analyzer.AsMap(analyzer.Inner(x), "limitCount") != nil {
					hasLimit = true
					return false
				}
			}
			return true
		})
		if hasLimit {
			return true
		}
		loc := firstLocation(wc)
		out = append(out, analyzer.Finding{
			Severity:    analyzer.SeverityWarn,
			Message:     "Recursive CTE without an explicit termination: consider a LIMIT or `WHERE depth < N` guard",
			Explanation: "WITH RECURSIVE will iterate until the UNION produces no new rows. On cyclic graphs or unexpected data, this loops forever — the query consumes memory and CPU until cancelled or it exhausts work_mem.",
			Suggestion:  "Add a depth counter and WHERE clause, or a final LIMIT to cap the total work.",
			Evidence:    "src/backend/executor/nodeRecursiveunion.c",
			Location:    analyzer.Range{Start: loc, End: loc + 1},
		})
		return true
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// union_vs_union_all — UNION may hide UNION ALL that is cheaper.
// ──────────────────────────────────────────────────────────────

type unionVsUnionAll struct{}

func (unionVsUnionAll) ID() string                        { return "union_vs_union_all" }
func (unionVsUnionAll) Description() string               { return "UNION removes duplicates via sort/hash; use UNION ALL if duplicates are impossible/irrelevant" }
func (unionVsUnionAll) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (unionVsUnionAll) RequiresSchema() bool              { return false }
func (unionVsUnionAll) RequiresExplain() bool             { return false }

func (unionVsUnionAll) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	forEachSelect(ctx.AST, func(sel analyzer.ASTNode) {
		op := analyzer.AsString(sel, "op")
		if op != "SETOP_UNION" {
			return
		}
		if all, ok := sel["all"]; ok && all == true {
			return
		}
		loc := firstLocation(sel)
		out = append(out, analyzer.Finding{
			Severity:    analyzer.SeverityInfo,
			Message:     "UNION deduplicates (sort/hash); prefer UNION ALL when duplicates are impossible or irrelevant",
			Explanation: "UNION appends + deduplicates; UNION ALL only appends. The dedup is done with a Sort + Unique or HashAggregate (see src/backend/executor/nodeSetOp.c), which can be expensive on large sets.",
			Suggestion:  "If the branches cannot produce duplicates (disjoint predicates, different sources), use UNION ALL.",
			Evidence:    "src/backend/executor/nodeSetOp.c",
			Location:    analyzer.Range{Start: loc, End: loc + 1},
		})
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// insert_without_target_list — INSERT INTO t VALUES (…) with no column list.
// ──────────────────────────────────────────────────────────────

type insertNoColList struct{}

func (insertNoColList) ID() string                        { return "insert_no_column_list" }
func (insertNoColList) Description() string               { return "INSERT without explicit column list breaks on schema changes" }
func (insertNoColList) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (insertNoColList) RequiresSchema() bool              { return false }
func (insertNoColList) RequiresExplain() bool             { return false }

func (insertNoColList) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	forEachStmt(ctx.AST, func(kind string, inner, wrap analyzer.ASTNode) {
		if kind != "InsertStmt" {
			return
		}
		if cols := analyzer.AsList(inner, "cols"); len(cols) > 0 {
			return
		}
		loc := firstLocation(inner)
		out = append(out, analyzer.Finding{
			Severity:    analyzer.SeverityInfo,
			Message:     "INSERT has no explicit column list; adding a column to the table will break this statement",
			Explanation: "Without a column list, VALUES are positional against the table's current column order. ALTER TABLE ADD COLUMN silently changes positions; existing INSERTs start failing or inserting into the wrong columns.",
			Suggestion:  "Write `INSERT INTO t (col1, col2, …) VALUES (…)` even for single-row inserts.",
			Evidence:    "src/backend/parser/analyze.c (transformInsertStmt)",
			Location:    analyzer.Range{Start: loc, End: loc + 1},
		})
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// delete_without_using_but_correlated_subquery — DELETE WHERE EXISTS(sub)
// when USING would be clearer and sometimes faster.
// (Informational; harder to detect robustly.)
// ──────────────────────────────────────────────────────────────

type deleteCorrelatedSub struct{}

func (deleteCorrelatedSub) ID() string                        { return "delete_correlated_subquery" }
func (deleteCorrelatedSub) Description() string               { return "DELETE ... WHERE EXISTS (correlated sub) often cleaner as DELETE USING" }
func (deleteCorrelatedSub) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (deleteCorrelatedSub) RequiresSchema() bool              { return false }
func (deleteCorrelatedSub) RequiresExplain() bool             { return false }

func (deleteCorrelatedSub) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	forEachStmt(ctx.AST, func(kind string, inner, wrap analyzer.ASTNode) {
		if kind != "DeleteStmt" {
			return
		}
		where := analyzer.AsMap(inner, "whereClause")
		if where == nil {
			return
		}
		has := false
		analyzer.Walk(where, func(_ []string, n analyzer.ASTNode) bool {
			if analyzer.NodeKind(n) == "SubLink" {
				if analyzer.AsString(analyzer.Inner(n), "subLinkType") == "EXISTS_SUBLINK" {
					has = true
					return false
				}
			}
			return true
		})
		if !has {
			return
		}
		out = append(out, analyzer.Finding{
			Severity:    analyzer.SeverityInfo,
			Message:     "DELETE ... WHERE EXISTS(correlated sub) — `DELETE ... USING` is often more readable and sometimes faster",
			Explanation: "DELETE supports a USING clause that joins additional tables just like a SELECT. The planner's reach is typically the same, but the intent is clearer and it avoids correlated-subquery quirks in complex DELETEs.",
			Suggestion:  "Rewrite as `DELETE FROM t USING other o WHERE t.x = o.x AND o.condition`.",
			Evidence:    "SQL syntax in src/backend/parser/gram.y",
			Location:    analyzer.Range{Start: firstLocation(where), End: firstLocation(where) + 1},
		})
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// default_statistics_query_hint — helpful info, no rule body here.
// ──────────────────────────────────────────────────────────────

// printf dance to silence unused imports guard when trimming rules during dev:
var _ = fmt.Sprintf

func init() {
	Register(countStarBig{})
	Register(orderByOrdinal{})
	Register(groupByOrdinal{})
	Register(implicitCrossJoin{})
	Register(booleanEqualsTrue{})
	Register(coalesceInWhere{})
	Register(orderByRandom{})
	Register(subqueryInSelect{})
	Register(windowEmpty{})
	Register(truncateInTx{})
	Register(intervalArithmetic{})
	Register(recursiveCTENoLimit{})
	Register(unionVsUnionAll{})
	Register(insertNoColList{})
	Register(deleteCorrelatedSub{})
}
