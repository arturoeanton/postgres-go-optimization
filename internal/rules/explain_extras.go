package rules

import (
	"fmt"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
	"github.com/arturoeanton/postgres-go-optimization/internal/explain"
)

// Additional EXPLAIN-based rules.

// ──────────────────────────────────────────────────────────────
// nested_loop_seq_inner — NL with a Seq Scan as inner and loops>1.
// Often the pathology behind slow joins.
// ──────────────────────────────────────────────────────────────

type nestedLoopSeqInner struct{}

func (nestedLoopSeqInner) ID() string                        { return "explain_nestloop_seq_inner" }
func (nestedLoopSeqInner) Description() string               { return "Nested Loop with Seq Scan inner that runs many loops — N×M disaster" }
func (nestedLoopSeqInner) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (nestedLoopSeqInner) RequiresSchema() bool              { return false }
func (nestedLoopSeqInner) RequiresExplain() bool             { return true }

func (nestedLoopSeqInner) Check(ctx *analyzer.Context) []analyzer.Finding {
	if ctx.Explain == nil {
		return nil
	}
	var out []analyzer.Finding
	ctx.Explain.Walk(func(n *explain.Node) {
		if n.NodeType != "Nested Loop" || len(n.Plans) != 2 {
			return
		}
		inner := n.Plans[1]
		if inner.NodeType != "Seq Scan" {
			return
		}
		if inner.ActualLoops < 100 {
			return
		}
		out = append(out, analyzer.Finding{
			Severity: analyzer.SeverityWarn,
			Message: fmt.Sprintf("Nested Loop over Seq Scan on %s, %d loops — expected %0.fx scans of the inner",
				inner.RelationName, int(inner.ActualLoops), inner.ActualLoops),
			Explanation: "Nested Loop rescans its inner once per outer tuple. A Seq Scan inner means the entire table is scanned N times where N is the outer's row count. With N=1000s this is typically the dominant cost of the query.",
			Suggestion:  "Add an index that the inner can use as a parameterized path; or force a hash/merge join method (see pg_plan_advice HASH_JOIN/MERGE_JOIN advice in this fork).",
			Evidence:    "src/backend/executor/nodeNestloop.c; GUIA_POSTGRES_ES_2.md §23.1",
		})
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// parallel_underused — Workers Launched < Workers Planned.
// ──────────────────────────────────────────────────────────────

type parallelUnderused struct{}

func (parallelUnderused) ID() string                        { return "explain_parallel_underused" }
func (parallelUnderused) Description() string               { return "Fewer parallel workers launched than planned — pool is saturated" }
func (parallelUnderused) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (parallelUnderused) RequiresSchema() bool              { return false }
func (parallelUnderused) RequiresExplain() bool             { return true }

func (parallelUnderused) Check(ctx *analyzer.Context) []analyzer.Finding {
	if ctx.Explain == nil {
		return nil
	}
	var out []analyzer.Finding
	ctx.Explain.Walk(func(n *explain.Node) {
		if n.WorkersPlanned == 0 {
			return
		}
		if n.WorkersLaunched >= n.WorkersPlanned {
			return
		}
		out = append(out, analyzer.Finding{
			Severity: analyzer.SeverityInfo,
			Message: fmt.Sprintf("%s planned %d workers but launched %d",
				n.NodeType, n.WorkersPlanned, n.WorkersLaunched),
			Explanation: "The planner reserved worker slots but the pool (`max_parallel_workers`, `max_parallel_workers_per_gather`) could not satisfy them. Parallel sections then run with fewer workers, or serially.",
			Suggestion:  "Increase `max_parallel_workers` and `max_parallel_workers_per_gather` in postgresql.conf. Monitor `pg_stat_activity` during the query to see how many are in use.",
			Evidence:    "src/backend/access/transam/parallel.c",
		})
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// temp_buffers_used — any node that wrote temp blocks.
// ──────────────────────────────────────────────────────────────

type tempBuffersUsed struct{}

func (tempBuffersUsed) ID() string                        { return "explain_temp_buffers" }
func (tempBuffersUsed) Description() string               { return "Node used temp files — something spilled to disk" }
func (tempBuffersUsed) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (tempBuffersUsed) RequiresSchema() bool              { return false }
func (tempBuffersUsed) RequiresExplain() bool             { return true }

func (tempBuffersUsed) Check(ctx *analyzer.Context) []analyzer.Finding {
	if ctx.Explain == nil {
		return nil
	}
	var out []analyzer.Finding
	seen := map[string]bool{}
	ctx.Explain.Walk(func(n *explain.Node) {
		if n.TempReadBlocks == 0 && n.TempWriteBlocks == 0 {
			return
		}
		key := n.NodeType
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, analyzer.Finding{
			Severity: analyzer.SeverityWarn,
			Message: fmt.Sprintf("%s wrote %d and read %d temp blocks (%.1f MB total)",
				n.NodeType, n.TempWriteBlocks, n.TempReadBlocks,
				float64(n.TempReadBlocks+n.TempWriteBlocks)*8.0/1024.0),
			Explanation: "Temp blocks mean the operation didn't fit work_mem. Sort switches to external merge; HashAgg/HashJoin partition to disk; tuplestore spills. Each spill doubles or more the I/O of the operation.",
			Suggestion:  "For this query: SET LOCAL work_mem = '256MB' (scale to the reported temp usage). Avoid setting work_mem globally high — it multiplies by ops × connections.",
			Evidence:    "src/backend/utils/sort/tuplesort.c; src/backend/executor/nodeHash.c",
		})
	})
	return out
}

// ──────────────────────────────────────────────────────────────
// cold_cache — shared_read >> shared_hit across the plan.
// ──────────────────────────────────────────────────────────────

type coldCache struct{}

func (coldCache) ID() string                        { return "explain_cold_cache" }
func (coldCache) Description() string               { return "Most pages read from disk — cold cache or shared_buffers too small" }
func (coldCache) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (coldCache) RequiresSchema() bool              { return false }
func (coldCache) RequiresExplain() bool             { return true }

func (coldCache) Check(ctx *analyzer.Context) []analyzer.Finding {
	if ctx.Explain == nil {
		return nil
	}
	var hit, read int64
	ctx.Explain.Walk(func(n *explain.Node) {
		hit += n.SharedHitBlocks
		read += n.SharedReadBlocks
	})
	total := hit + read
	if total < 1000 { // too small to judge
		return nil
	}
	if float64(read)/float64(total) < 0.5 {
		return nil
	}
	return []analyzer.Finding{{
		Severity: analyzer.SeverityInfo,
		Message: fmt.Sprintf("Cache hit ratio: %.1f%% (%d hit / %d read of %d total blocks)",
			100*float64(hit)/float64(total), hit, read, total),
		Explanation: "Most pages were fetched from the OS page cache or disk, not from shared_buffers. Either the first run (cold cache is inevitable) or shared_buffers cannot hold the working set. On a warm cluster the ratio should be 95%+.",
		Suggestion:  "Run the query again to rule out cold-start. If still low, consider raising shared_buffers (aim: ~25% of RAM) and using pg_prewarm to keep hot relations pinned.",
		Evidence:    "src/backend/storage/buffer/bufmgr.c",
	}}
}

func init() {
	Register(nestedLoopSeqInner{})
	Register(parallelUnderused{})
	Register(tempBuffersUsed{})
	Register(coldCache{})
}
