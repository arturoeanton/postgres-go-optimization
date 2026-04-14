package analyzer

import (
	"strings"

	"github.com/arturoeanton/postgres-go-optimization/internal/explain"
	"github.com/arturoeanton/postgres-go-optimization/internal/schema"
)

// Context holds everything a Rule needs to analyze a query.
type Context struct {
	// Source is the original SQL text (as user provided).
	Source string

	// AST is the JSON-parsed tree (pg_query.ParseToJSON).
	AST ASTNode

	// Schema is the (optional) live schema loaded from a database.
	Schema *schema.Schema

	// Explain is the (optional) parsed EXPLAIN (ANALYZE, BUFFERS) JSON plan.
	Explain *explain.Plan
}

// Snippet returns a safe substring of the source between [start, end).
// If the range is invalid or out of bounds, returns "".
func (c *Context) Snippet(r Range) string {
	if r.Start < 0 || r.End <= r.Start || r.End > len(c.Source) {
		return ""
	}
	s := c.Source[r.Start:r.End]
	return strings.TrimSpace(s)
}

// LineCol converts a 0-indexed byte offset to 1-indexed (line, column).
func (c *Context) LineCol(off int) (line, col int) {
	line = 1
	col = 1
	if off < 0 || off > len(c.Source) {
		return
	}
	for i := 0; i < off; i++ {
		if c.Source[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return
}
