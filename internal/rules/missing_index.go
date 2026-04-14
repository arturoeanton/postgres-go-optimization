package rules

import (
	"strings"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
	"github.com/arturoeanton/postgres-go-optimization/internal/schema"
)

// missingIndex (schema-aware) walks WHERE clauses and flags equality or
// range predicates on columns of tables that have no btree-compatible
// index whose leading column matches.
//
// The rule is intentionally conservative: it only suggests when we are
// confident no usable index exists. We don't second-guess covering
// strategies (multi-column, partial, expression) — creating those is a
// design decision that needs human judgment.
type missingIndex struct{}

func (missingIndex) ID() string                        { return "missing_index" }
func (missingIndex) Description() string               { return "No index on WHERE column (schema-aware)" }
func (missingIndex) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (missingIndex) RequiresSchema() bool              { return true }
func (missingIndex) RequiresExplain() bool             { return false }

// btree-indexable comparison operators
var indexableOps = map[string]struct{}{
	"=": {}, "<": {}, "<=": {}, ">": {}, ">=": {}, "<>": {},
}

func (missingIndex) Check(ctx *analyzer.Context) []analyzer.Finding {
	if ctx.Schema == nil {
		return nil
	}
	tables := collectFromTables(ctx.AST) // map[alias]fullName
	var out []analyzer.Finding
	seen := map[string]bool{}

	walkWhereClauses(ctx.AST, func(where analyzer.ASTNode) {
		analyzer.Walk(where, func(path []string, n analyzer.ASTNode) bool {
			if analyzer.NodeKind(n) != "A_Expr" {
				return true
			}
			e := analyzer.Inner(n)
			if analyzer.AsString(e, "kind") != "AEXPR_OP" {
				return true
			}
			op := extractOpName(analyzer.AsList(e, "name"))
			if _, ok := indexableOps[op]; !ok {
				return true
			}
			l := analyzer.AsMap(e, "lexpr")
			if analyzer.NodeKind(l) != "ColumnRef" {
				return true
			}
			qualifier, colName := splitColumn(l)
			tableRef := resolveTable(qualifier, tables)
			if tableRef == "" {
				return true
			}
			rel := ctx.Schema.Lookup(tableRef)
			if rel == nil {
				return true
			}
			if leadingIndex(rel, colName) {
				return true
			}
			// Only suggest indexes on tables with enough data to matter.
			if rel.LiveTuples > 0 && rel.LiveTuples < 1000 {
				return true
			}
			key := rel.Schema + "." + rel.Name + "." + colName
			if seen[key] {
				return true
			}
			seen[key] = true
			loc := analyzer.AsInt(e, "location")
			out = append(out, analyzer.Finding{
				Severity: analyzer.SeverityWarn,
				Message: "No index on `" + rel.Schema + "." + rel.Name + "(" + colName +
					")` for predicate `" + op + "`",
				Explanation: "The planner chooses index paths in src/backend/optimizer/path/indxpath.c. Without an index " +
					"whose leading column matches the predicate, the only option is a Seq Scan + Filter " +
					"(src/backend/executor/nodeSeqscan.c). For selective predicates on large tables this is expensive.",
				Suggestion: "CREATE INDEX ON " + rel.Schema + "." + rel.Name + " (" + colName + "); " +
					"for composite predicates, consider multi-column or partial/expression indexes.",
				Evidence: "src/backend/optimizer/path/indxpath.c; GUIA_POSTGRES_ES_2.md §26",
				Location: analyzer.Range{Start: loc, End: loc + 1},
			})
			return true
		})
	})
	return out
}

// collectFromTables returns alias -> qualified name, using RangeVars.
func collectFromTables(ast analyzer.ASTNode) map[string]string {
	out := map[string]string{}
	analyzer.Walk(ast, func(path []string, n analyzer.ASTNode) bool {
		if analyzer.NodeKind(n) != "RangeVar" {
			return true
		}
		rv := analyzer.Inner(n)
		name := analyzer.AsString(rv, "relname")
		alias := ""
		if a := analyzer.AsMap(rv, "alias"); a != nil {
			alias = analyzer.AsString(a, "aliasname")
		}
		if alias == "" {
			alias = name
		}
		sch := analyzer.AsString(rv, "schemaname")
		full := name
		if sch != "" {
			full = sch + "." + name
		}
		out[alias] = full
		return true
	})
	return out
}

func splitColumn(colref analyzer.ASTNode) (qualifier, name string) {
	cr := analyzer.Inner(colref)
	fields := analyzer.AsList(cr, "fields")
	var parts []string
	for _, f := range fields {
		if m, ok := f.(analyzer.ASTNode); ok {
			if analyzer.NodeKind(m) == "String" {
				parts = append(parts, analyzer.AsString(analyzer.Inner(m), "sval"))
			}
		}
	}
	switch len(parts) {
	case 0:
		return "", ""
	case 1:
		return "", parts[0]
	default:
		return strings.Join(parts[:len(parts)-1], "."), parts[len(parts)-1]
	}
}

func resolveTable(alias string, tables map[string]string) string {
	if alias == "" {
		if len(tables) == 1 {
			for _, t := range tables {
				return t
			}
		}
		return ""
	}
	return tables[alias]
}

// leadingIndex reports whether col is the first key of some index
// (btree or the column of a primary key).
func leadingIndex(rel *schema.Relation, col string) bool {
	for _, idx := range rel.Indexes {
		if len(idx.Columns) == 0 {
			continue
		}
		if strings.EqualFold(idx.Columns[0], col) {
			// Only btree / hash cover equality+range generically.
			switch idx.Method {
			case "btree", "hash":
				return true
			}
		}
	}
	return false
}

func init() { Register(missingIndex{}) }
