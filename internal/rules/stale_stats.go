package rules

import (
	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// staleStats flags relations referenced by the query whose last autoanalyze
// is NULL (never analyzed) or whose dead-tuple ratio is high. Both indicate
// stats that may lie to the planner.
type staleStats struct{}

func (staleStats) ID() string                        { return "stale_stats" }
func (staleStats) Description() string               { return "Table stats may be stale (never analyzed or high dead-ratio)" }
func (staleStats) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (staleStats) RequiresSchema() bool              { return true }
func (staleStats) RequiresExplain() bool             { return false }

func (staleStats) Check(ctx *analyzer.Context) []analyzer.Finding {
	if ctx.Schema == nil {
		return nil
	}
	tables := collectFromTables(ctx.AST)
	seen := map[string]bool{}
	var out []analyzer.Finding
	for _, full := range tables {
		if seen[full] {
			continue
		}
		seen[full] = true
		rel := ctx.Schema.Lookup(full)
		if rel == nil {
			continue
		}
		if rel.LiveTuples < 1000 {
			continue
		}
		if rel.LastAnalyze == nil {
			out = append(out, analyzer.Finding{
				Severity: analyzer.SeverityInfo,
				Message:  "Table `" + rel.Schema + "." + rel.Name + "` has never been (auto)analyzed",
				Explanation: "Stats in pg_statistic drive every selectivity estimate. Without them the planner uses " +
					"placeholder defaults that are often wildly off, producing bad plans.",
				Suggestion: "ANALYZE " + rel.Schema + "." + rel.Name + ";",
				Evidence:   "src/backend/commands/analyze.c; GUIA_POSTGRES_ES_2.md §17",
			})
			continue
		}
		if rel.NDeadRatio > 0.2 {
			out = append(out, analyzer.Finding{
				Severity: analyzer.SeverityInfo,
				Message: "Table `" + rel.Schema + "." + rel.Name + "` has high dead-tuple ratio",
				Explanation: "n_dead_tup/n_live_tup > 20%. Autovacuum may be behind. Dead tuples bloat the table, " +
					"slow sequential scans, and can leave visibility map stale (hurting Index-Only Scan).",
				Suggestion: "VACUUM (ANALYZE) " + rel.Schema + "." + rel.Name + "; consider ALTER TABLE ... SET " +
					"(autovacuum_vacuum_scale_factor = 0.02) for large, hot tables.",
				Evidence: "src/backend/postmaster/autovacuum.c; GUIA_POSTGRES_ES_2.md §28",
			})
		}
	}
	return out
}

func init() { Register(staleStats{}) }
