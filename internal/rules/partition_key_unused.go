package rules

import (
	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// partitionKeyUnused flags queries that reference a partitioned table but
// do not filter on the partition key. Without that filter, partition
// pruning (src/backend/partitioning/partprune.c) cannot discard any
// partitions and the query ends up scanning them all.
//
// Note: this rule uses a heuristic to collect column references in WHERE
// (not full predicate analysis). False negatives are possible when keys
// are tested only via joins; false positives when non-trivial expressions
// involving the key slip past our simple matcher.
type partitionKeyUnused struct{}

func (partitionKeyUnused) ID() string                        { return "partition_key_unused" }
func (partitionKeyUnused) Description() string               { return "Query on partitioned table without filter on partition key" }
func (partitionKeyUnused) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (partitionKeyUnused) RequiresSchema() bool              { return true }
func (partitionKeyUnused) RequiresExplain() bool             { return false }

func (partitionKeyUnused) Check(ctx *analyzer.Context) []analyzer.Finding {
	if ctx.Schema == nil {
		return nil
	}
	tables := collectFromTables(ctx.AST)
	referenced := collectWhereColumns(ctx.AST) // set of bare column names used in WHERE

	var out []analyzer.Finding
	seen := map[string]bool{}

	for _, full := range tables {
		if seen[full] {
			continue
		}
		seen[full] = true
		rel := ctx.Schema.Lookup(full)
		if rel == nil || rel.Kind != "p" {
			continue
		}
		if len(rel.PartKeys) == 0 {
			// loader currently doesn't populate partition keys; warn unconditionally
			// but tone down to info — user still learns something useful.
			out = append(out, analyzer.Finding{
				Severity: analyzer.SeverityInfo,
				Message:  "Query touches partitioned table `" + rel.Schema + "." + rel.Name + "` — verify a partition-key filter exists",
				Explanation: "If the WHERE does not restrict the partition key, partition pruning cannot eliminate any " +
					"child; the planner expands to all partitions. For range-partitioned tables that usually means " +
					"scanning orders of magnitude more data than needed.",
				Suggestion: "Add a WHERE clause on the partition key (e.g. `ts >= ... AND ts < ...` for time-range partitions).",
				Evidence:   "src/backend/partitioning/partprune.c; GUIA_POSTGRES_ES_2.md §29",
			})
			continue
		}
		usedKey := false
		for _, k := range rel.PartKeys {
			if referenced[k] {
				usedKey = true
				break
			}
		}
		if usedKey {
			continue
		}
		out = append(out, analyzer.Finding{
			Severity: analyzer.SeverityWarn,
			Message: "Partitioned table `" + rel.Schema + "." + rel.Name + "`: WHERE does not filter on partition key(s)",
			Explanation: "Partition pruning (src/backend/partitioning/partprune.c) relies on WHERE clauses on the " +
				"partition key to skip partitions at plan or execution time. Without that, every partition is scanned.",
			Suggestion: "Add a filter on the partition key(s) to enable pruning.",
			Evidence:   "src/backend/partitioning/partprune.c",
		})
	}
	return out
}

// collectWhereColumns returns a set of bare column names referenced anywhere
// in any WHERE clause of the statement.
func collectWhereColumns(ast analyzer.ASTNode) map[string]bool {
	out := map[string]bool{}
	walkWhereClauses(ast, func(where analyzer.ASTNode) {
		analyzer.Walk(where, func(path []string, n analyzer.ASTNode) bool {
			if analyzer.NodeKind(n) != "ColumnRef" {
				return true
			}
			_, col := splitColumn(n)
			if col != "" {
				out[col] = true
			}
			return true
		})
	})
	return out
}

func init() { Register(partitionKeyUnused{}) }
