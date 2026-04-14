package rules

import (
	"strings"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// likeLeadingWildcard flags `col LIKE '%foo'` and `col LIKE '%foo%'` and
// suggests `pg_trgm` with a GIN/GiST index, or full-text search.
//
// A B-tree index on a text column orders values lexicographically; a
// leading-wildcard LIKE cannot exploit that order. `col LIKE 'foo%'` IS
// sargable on a btree with `text_pattern_ops` (or the C collation).
type likeLeadingWildcard struct{}

func (likeLeadingWildcard) ID() string                        { return "like_leading_wildcard" }
func (likeLeadingWildcard) Description() string               { return "LIKE '%...' cannot use a plain btree index; use pg_trgm+GIN" }
func (likeLeadingWildcard) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (likeLeadingWildcard) RequiresSchema() bool              { return false }
func (likeLeadingWildcard) RequiresExplain() bool             { return false }

func (likeLeadingWildcard) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	analyzer.Walk(ctx.AST, func(path []string, n analyzer.ASTNode) bool {
		if analyzer.NodeKind(n) != "A_Expr" {
			return true
		}
		e := analyzer.Inner(n)
		// Match: name ["~~"] (LIKE) or ["~~*"] (ILIKE). kind == "AEXPR_LIKE" or "AEXPR_ILIKE".
		kind := analyzer.AsString(e, "kind")
		if kind != "AEXPR_LIKE" && kind != "AEXPR_ILIKE" && kind != "AEXPR_OP" {
			return true
		}
		names := analyzer.AsList(e, "name")
		op := ""
		for _, nm := range names {
			if mm, ok := nm.(analyzer.ASTNode); ok {
				op = analyzer.AsString(analyzer.Inner(mm), "sval")
			}
		}
		if op != "~~" && op != "~~*" && op != "!~~" && op != "!~~*" {
			return true
		}
		// rexpr should be a string A_Const.
		r := analyzer.AsMap(e, "rexpr")
		if analyzer.NodeKind(r) != "A_Const" {
			return true
		}
		sv := analyzer.AsMap(analyzer.Inner(r), "sval")
		pattern := analyzer.AsString(sv, "sval")
		if !strings.HasPrefix(pattern, "%") {
			return true
		}
		loc := analyzer.AsInt(e, "location")
		out = append(out, analyzer.Finding{
			Severity: analyzer.SeverityWarn,
			Message:  "LIKE pattern with leading `%` cannot use a btree index",
			Explanation: "A btree is ordered lexicographically; `'%foo'` has no fixed prefix so there's no range to scan. " +
				"The pg_trgm extension provides trigram indexing via GIN or GiST that supports substring search. " +
				"See src/backend/access/gin/ and contrib/pg_trgm/.",
			Suggestion: "CREATE EXTENSION IF NOT EXISTS pg_trgm; CREATE INDEX ON t USING GIN (col gin_trgm_ops); " +
				"or use full-text search with tsvector if the content is natural language.",
			Evidence: "contrib/pg_trgm/README; GUIA_POSTGRES_ES_2.md §27.1",
			Location: analyzer.Range{Start: loc, End: loc + 1},
		})
		return true
	})
	return out
}

func init() { Register(likeLeadingWildcard{}) }
