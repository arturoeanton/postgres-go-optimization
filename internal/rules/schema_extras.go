package rules

import (
	"sort"
	"strings"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
	"github.com/arturoeanton/postgres-go-optimization/internal/schema"
)

// Schema-aware rules that inspect the whole relation/index graph rather
// than individual WHERE clauses. These fire whenever ANY relation in the
// query matches the pattern — so running `pgopt --db` on any query that
// touches a problematic table surfaces the issue.

// ──────────────────────────────────────────────────────────────
// fk_without_index — a foreign key whose local columns are not indexed.
// ──────────────────────────────────────────────────────────────

type fkWithoutIndex struct{}

func (fkWithoutIndex) ID() string                        { return "fk_without_index" }
func (fkWithoutIndex) Description() string               { return "Foreign-key column is not indexed (slow deletes on parent, lock escalations)" }
func (fkWithoutIndex) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (fkWithoutIndex) RequiresSchema() bool              { return true }
func (fkWithoutIndex) RequiresExplain() bool             { return false }

func (fkWithoutIndex) Check(ctx *analyzer.Context) []analyzer.Finding {
	if ctx.Schema == nil {
		return nil
	}
	var out []analyzer.Finding
	seen := map[string]bool{}
	for _, full := range collectFromTables(ctx.AST) {
		rel := ctx.Schema.Lookup(full)
		if rel == nil {
			continue
		}
		for _, fk := range rel.ForeignKeys {
			key := rel.Schema + "." + rel.Name + "." + strings.Join(fk.Columns, ",")
			if seen[key] {
				continue
			}
			seen[key] = true
			if indexCoversColumns(rel, fk.Columns) {
				continue
			}
			out = append(out, analyzer.Finding{
				Severity: analyzer.SeverityWarn,
				Message: "Foreign key `" + rel.Schema + "." + rel.Name + "(" + strings.Join(fk.Columns, ",") +
					")` has no supporting index",
				Explanation: "PostgreSQL enforces FKs by looking up parent rows on UPDATE/DELETE of the parent. Without an index " +
					"on the child's FK column, every parent change scans the child table sequentially (see " +
					"RI_FKey_* functions in src/backend/utils/adt/ri_triggers.c). Also, ON DELETE CASCADE becomes O(parent×child).",
				Suggestion: "CREATE INDEX ON " + rel.Schema + "." + rel.Name + " (" + strings.Join(fk.Columns, ",") + ");",
				Evidence:   "src/backend/utils/adt/ri_triggers.c",
			})
		}
	}
	return out
}

func indexCoversColumns(rel *schema.Relation, cols []string) bool {
	for _, idx := range rel.Indexes {
		if len(idx.Columns) < len(cols) {
			continue
		}
		match := true
		for i, c := range cols {
			if !strings.EqualFold(idx.Columns[i], c) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// ──────────────────────────────────────────────────────────────
// redundant_index — one index's key set is a prefix of another's.
// ──────────────────────────────────────────────────────────────

type redundantIndex struct{}

func (redundantIndex) ID() string                        { return "redundant_index" }
func (redundantIndex) Description() string               { return "Redundant index: its key columns are a strict prefix of another index" }
func (redundantIndex) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (redundantIndex) RequiresSchema() bool              { return true }
func (redundantIndex) RequiresExplain() bool             { return false }

func (redundantIndex) Check(ctx *analyzer.Context) []analyzer.Finding {
	if ctx.Schema == nil {
		return nil
	}
	var out []analyzer.Finding
	seen := map[string]bool{}
	for _, full := range collectFromTables(ctx.AST) {
		rel := ctx.Schema.Lookup(full)
		if rel == nil || seen[full] {
			continue
		}
		seen[full] = true
		for i, a := range rel.Indexes {
			for j, b := range rel.Indexes {
				if i == j || a.Method != b.Method || a.Primary || a.Unique {
					continue
				}
				if isStrictPrefix(a.Columns, b.Columns) {
					out = append(out, analyzer.Finding{
						Severity: analyzer.SeverityInfo,
						Message: "Index `" + a.Name + "` on " + rel.Name + " is a strict prefix of `" + b.Name +
							"` and is likely redundant",
						Explanation: "When index (a) is `(c1, c2, …, ck)` and index (b) is `(c1, c2, …, ck, c_{k+1}, …)`, " +
							"index (b) can answer every query that (a) answers. Keeping both costs write amplification " +
							"and bloat with no read benefit.",
						Suggestion: "Consider DROP INDEX " + a.Name + "; if nothing relies on its particular shape.",
						Evidence:   "General indexing principle; cross-verify with pg_stat_user_indexes.idx_scan",
					})
				}
			}
		}
	}
	return out
}

// ──────────────────────────────────────────────────────────────
// duplicate_index — two indexes on identical column sets.
// ──────────────────────────────────────────────────────────────

type duplicateIndex struct{}

func (duplicateIndex) ID() string                        { return "duplicate_index" }
func (duplicateIndex) Description() string               { return "Duplicate index: two indexes cover the same column set" }
func (duplicateIndex) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (duplicateIndex) RequiresSchema() bool              { return true }
func (duplicateIndex) RequiresExplain() bool             { return false }

func (duplicateIndex) Check(ctx *analyzer.Context) []analyzer.Finding {
	if ctx.Schema == nil {
		return nil
	}
	var out []analyzer.Finding
	seen := map[string]bool{}
	for _, full := range collectFromTables(ctx.AST) {
		rel := ctx.Schema.Lookup(full)
		if rel == nil || seen[full] {
			continue
		}
		seen[full] = true
		pairsReported := map[string]bool{}
		for i, a := range rel.Indexes {
			for j, b := range rel.Indexes {
				if i >= j {
					continue
				}
				if a.Method != b.Method {
					continue
				}
				if !sameCols(a.Columns, b.Columns) {
					continue
				}
				key := a.Name + "|" + b.Name
				if pairsReported[key] {
					continue
				}
				pairsReported[key] = true
				out = append(out, analyzer.Finding{
					Severity:    analyzer.SeverityWarn,
					Message:     "Duplicate indexes on " + rel.Name + ": `" + a.Name + "` and `" + b.Name + "` cover identical columns",
					Explanation: "Two indexes on the same columns with the same AM give no additional query coverage — they double the write cost and double the space used. Worst case: you're paying for both and only one is ever chosen by the planner.",
					Suggestion:  "DROP one of them. Check pg_stat_user_indexes.idx_scan to see which is actually used.",
					Evidence:    "General indexing principle",
				})
			}
		}
	}
	return out
}

// ──────────────────────────────────────────────────────────────
// unused_index — an index with zero recorded scans.
// ──────────────────────────────────────────────────────────────

type unusedIndex struct{}

func (unusedIndex) ID() string                        { return "unused_index" }
func (unusedIndex) Description() string               { return "Index never scanned (pg_stat_user_indexes.idx_scan = 0)" }
func (unusedIndex) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (unusedIndex) RequiresSchema() bool              { return true }
func (unusedIndex) RequiresExplain() bool             { return false }

func (unusedIndex) Check(ctx *analyzer.Context) []analyzer.Finding {
	if ctx.Schema == nil {
		return nil
	}
	var out []analyzer.Finding
	seen := map[string]bool{}
	for _, full := range collectFromTables(ctx.AST) {
		rel := ctx.Schema.Lookup(full)
		if rel == nil || seen[full] {
			continue
		}
		seen[full] = true
		if rel.LiveTuples < 10000 {
			continue // small tables: stats not meaningful
		}
		// stable order so diagnostics are deterministic
		names := make([]string, 0, len(rel.IndexScans))
		for n := range rel.IndexScans {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if rel.IndexScans[n] > 0 {
				continue
			}
			// Don't recommend dropping primary/unique: they enforce constraints.
			skip := false
			for _, idx := range rel.Indexes {
				if idx.Name == n && (idx.Primary || idx.Unique) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			out = append(out, analyzer.Finding{
				Severity:    analyzer.SeverityInfo,
				Message:     "Index `" + n + "` on " + rel.Schema + "." + rel.Name + " has 0 recorded scans",
				Explanation: "pg_stat_user_indexes tracks idx_scan since the last stats reset. 0 scans for a long-lived cluster strongly suggests the index is never used by the planner. Each unused index still pays write-amp on every insert/update that touches its columns.",
				Suggestion:  "Verify uptime since last pg_stat_reset; if the index is genuinely never used, DROP INDEX " + n + ";",
				Evidence:    "pg_stat_user_indexes view (src/backend/utils/adt/pgstatfuncs.c)",
			})
		}
	}
	return out
}

// ──────────────────────────────────────────────────────────────
// missing_primary_key — a regular (kind='r') table without PK.
// ──────────────────────────────────────────────────────────────

type missingPrimaryKey struct{}

func (missingPrimaryKey) ID() string                        { return "missing_primary_key" }
func (missingPrimaryKey) Description() string               { return "Table has no primary key" }
func (missingPrimaryKey) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (missingPrimaryKey) RequiresSchema() bool              { return true }
func (missingPrimaryKey) RequiresExplain() bool             { return false }

func (missingPrimaryKey) Check(ctx *analyzer.Context) []analyzer.Finding {
	if ctx.Schema == nil {
		return nil
	}
	var out []analyzer.Finding
	seen := map[string]bool{}
	for _, full := range collectFromTables(ctx.AST) {
		rel := ctx.Schema.Lookup(full)
		if rel == nil || rel.Kind != "r" || seen[full] {
			continue
		}
		seen[full] = true
		if rel.HasPrimary {
			continue
		}
		out = append(out, analyzer.Finding{
			Severity:    analyzer.SeverityWarn,
			Message:     "Table `" + rel.Schema + "." + rel.Name + "` has no PRIMARY KEY",
			Explanation: "A PK defines row identity (important for logical replication, ORMs, audit trails) and creates a unique btree index automatically. Tables without PKs accumulate technical debt and break tooling assumptions.",
			Suggestion:  "ALTER TABLE " + rel.Schema + "." + rel.Name + " ADD PRIMARY KEY (id); — or on a natural unique combination.",
			Evidence:    "src/backend/catalog/heap.c; src/backend/replication/logical/",
		})
	}
	return out
}

// ──────────────────────────────────────────────────────────────
// timestamp_without_tz — columns typed `timestamp` (no tz).
// ──────────────────────────────────────────────────────────────

type timestampWithoutTZ struct{}

func (timestampWithoutTZ) ID() string                        { return "timestamp_without_tz" }
func (timestampWithoutTZ) Description() string               { return "Column typed `timestamp` (without time zone); use `timestamptz`" }
func (timestampWithoutTZ) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (timestampWithoutTZ) RequiresSchema() bool              { return true }
func (timestampWithoutTZ) RequiresExplain() bool             { return false }

func (timestampWithoutTZ) Check(ctx *analyzer.Context) []analyzer.Finding {
	if ctx.Schema == nil {
		return nil
	}
	var out []analyzer.Finding
	seen := map[string]bool{}
	for _, full := range collectFromTables(ctx.AST) {
		rel := ctx.Schema.Lookup(full)
		if rel == nil || seen[full] {
			continue
		}
		seen[full] = true
		for _, c := range rel.Columns {
			tn := strings.ToLower(c.TypeName)
			// canonical "timestamp without time zone" → type name includes
			// "without time zone". "timestamp with time zone" is the good one.
			if strings.HasPrefix(tn, "timestamp") && !strings.Contains(tn, "with time zone") {
				out = append(out, analyzer.Finding{
					Severity:    analyzer.SeverityInfo,
					Message:     "Column `" + rel.Name + "." + c.Name + "` is `timestamp without time zone` — prefer `timestamptz`",
					Explanation: "`timestamp` ignores the session's time zone on input, so the stored value depends on the client's locale. `timestamptz` stores absolute UTC and converts on display; it interoperates correctly across clients and zones.",
					Suggestion:  "ALTER TABLE " + rel.Schema + "." + rel.Name + " ALTER COLUMN " + c.Name + " TYPE timestamptz USING " + c.Name + " AT TIME ZONE 'UTC';",
					Evidence:    "src/backend/utils/adt/timestamp.c",
				})
			}
		}
	}
	return out
}

// Helpers for prefix/equal column comparison.

func isStrictPrefix(a, b []string) bool {
	if len(a) >= len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

func sameCols(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

func init() {
	Register(fkWithoutIndex{})
	Register(redundantIndex{})
	Register(duplicateIndex{})
	Register(unusedIndex{})
	Register(missingPrimaryKey{})
	Register(timestampWithoutTZ{})
}
