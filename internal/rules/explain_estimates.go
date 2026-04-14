package rules

import (
	"fmt"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
	"github.com/arturoeanton/postgres-go-optimization/internal/explain"
)

// explainEstimatesVsActual walks the EXPLAIN plan and flags nodes where
// the planner's estimated rows differ from actual rows by more than 10×.
// This is the #1 symptom of stale or missing statistics and should be
// fixed before any other tuning (see GUIA_POSTGRES_ES_2.md §33.3).
type explainEstimatesVsActual struct{}

func (explainEstimatesVsActual) ID() string           { return "explain_estimate_mismatch" }
func (explainEstimatesVsActual) Description() string {
	return "Planner estimate differs from actual rows by >10x — stats are wrong"
}
func (explainEstimatesVsActual) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (explainEstimatesVsActual) RequiresSchema() bool              { return false }
func (explainEstimatesVsActual) RequiresExplain() bool             { return true }

func (explainEstimatesVsActual) Check(ctx *analyzer.Context) []analyzer.Finding {
	if ctx.Explain == nil {
		return nil
	}
	var out []analyzer.Finding
	ctx.Explain.Walk(func(n *explain.Node) {
		if n.ActualLoops == 0 {
			return
		}
		actual := n.ActualRows * n.ActualLoops
		planned := n.PlanRows
		if planned < 1 {
			planned = 1
		}
		ratio := max(actual/planned, planned/actual)
		if ratio < 10 {
			return
		}
		out = append(out, analyzer.Finding{
			Severity: analyzer.SeverityWarn,
			Message: fmt.Sprintf("%s on %s: estimated %.0f rows, actual %.0f (%.1fx off)",
				n.NodeType, relOrIndex(n), planned, actual, ratio),
			Explanation: "Large estimate/actual mismatches drive bad plan choices. The planner uses pg_statistic " +
				"(MCV, histograms, correlation) and extended stats. See src/backend/utils/adt/selfuncs.c.",
			Suggestion: "Try: ANALYZE <table>; ALTER TABLE ... ALTER COLUMN ... SET STATISTICS 1000; " +
				"CREATE STATISTICS on correlated columns; re-run EXPLAIN.",
			Evidence: "src/backend/utils/adt/selfuncs.c; GUIA_POSTGRES_ES_2.md §17",
		})
	})
	return out
}

// explainExternalSort flags Sort nodes that spilled to disk.
type explainExternalSort struct{}

func (explainExternalSort) ID() string           { return "explain_external_sort" }
func (explainExternalSort) Description() string {
	return "Sort spilled to disk — work_mem too small for this operation"
}
func (explainExternalSort) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (explainExternalSort) RequiresSchema() bool              { return false }
func (explainExternalSort) RequiresExplain() bool             { return true }

func (explainExternalSort) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	ctx.Explain.Walk(func(n *explain.Node) {
		if n.NodeType != "Sort" {
			return
		}
		if n.SortSpaceType != "Disk" {
			return
		}
		out = append(out, analyzer.Finding{
			Severity: analyzer.SeverityWarn,
			Message: fmt.Sprintf("Sort spilled to disk: %s (%d kB)",
				n.SortMethod, n.SortSpaceUsed),
			Explanation: "When the sort set exceeds work_mem, tuplesort switches to external merge sort with " +
				"temp files (src/backend/utils/sort/tuplesort.c). That multiplies I/O.",
			Suggestion: "For this query: SET LOCAL work_mem = '256MB'; (scale to the sort size reported). " +
				"Avoid raising work_mem globally — it multiplies by (ops x connections).",
			Evidence: "src/backend/utils/sort/tuplesort.c; GUIA_POSTGRES_ES_2.md §33.3",
		})
	})
	return out
}

// explainHashBatches flags Hash Joins that partitioned to disk.
type explainHashBatches struct{}

func (explainHashBatches) ID() string           { return "explain_hash_batches" }
func (explainHashBatches) Description() string {
	return "Hash Join split into multiple batches — inner doesn't fit work_mem"
}
func (explainHashBatches) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (explainHashBatches) RequiresSchema() bool              { return false }
func (explainHashBatches) RequiresExplain() bool             { return true }

func (explainHashBatches) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	ctx.Explain.Walk(func(n *explain.Node) {
		if n.NodeType != "Hash" {
			return
		}
		if n.HashBatches <= 1 {
			return
		}
		out = append(out, analyzer.Finding{
			Severity: analyzer.SeverityWarn,
			Message: fmt.Sprintf("Hash built with %d batches (original %d)",
				n.HashBatches, n.OriginalHashBatches),
			Explanation: "Hash join partitions into batches when the inner doesn't fit work_mem " +
				"(src/backend/executor/nodeHash.c). Each batch rewrites inner+outer to tempfiles, doubling I/O.",
			Suggestion: "Raise work_mem locally for this query (SET LOCAL work_mem = '256MB';), or reduce the " +
				"inner's size with a more selective predicate / swap sides if the smaller one isn't inner.",
			Evidence: "src/backend/executor/nodeHash.c; GUIA_POSTGRES_ES_2.md §23.2",
		})
	})
	return out
}

// explainIOSHeapFetches flags Index-Only Scans with Heap Fetches > 0.
type explainIOSHeapFetches struct{}

func (explainIOSHeapFetches) ID() string           { return "explain_ios_heap_fetches" }
func (explainIOSHeapFetches) Description() string {
	return "Index-Only Scan had heap fetches — VM is stale"
}
func (explainIOSHeapFetches) DefaultSeverity() analyzer.Severity { return analyzer.SeverityInfo }
func (explainIOSHeapFetches) RequiresSchema() bool              { return false }
func (explainIOSHeapFetches) RequiresExplain() bool             { return true }

func (explainIOSHeapFetches) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	ctx.Explain.Walk(func(n *explain.Node) {
		if n.NodeType != "Index Only Scan" {
			return
		}
		if n.HeapFetches == 0 {
			return
		}
		out = append(out, analyzer.Finding{
			Severity: analyzer.SeverityInfo,
			Message: fmt.Sprintf("Index Only Scan on %s had %d heap fetches (expected 0)",
				n.RelationName, n.HeapFetches),
			Explanation: "IOS consults the Visibility Map (src/backend/access/heap/visibilitymap.c). When pages are " +
				"not marked all-visible, IOS falls back to heap fetches. This happens when VACUUM hasn't run recently.",
			Suggestion: "VACUUM (ANALYZE) " + n.RelationName + "; consider autovacuum_vacuum_insert_scale_factor " +
				"for insert-only tables.",
			Evidence: "src/backend/access/heap/visibilitymap.c; GUIA_POSTGRES_ES_2.md §8.2",
		})
	})
	return out
}

// explainSeqScanLargeTable flags Seq Scans over big relations with a
// highly selective Filter (most rows discarded).
type explainSeqScanLargeTable struct{}

func (explainSeqScanLargeTable) ID() string           { return "explain_seqscan_large" }
func (explainSeqScanLargeTable) Description() string {
	return "Seq Scan on a large table with highly selective filter — index candidate"
}
func (explainSeqScanLargeTable) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (explainSeqScanLargeTable) RequiresSchema() bool              { return false }
func (explainSeqScanLargeTable) RequiresExplain() bool             { return true }

func (explainSeqScanLargeTable) Check(ctx *analyzer.Context) []analyzer.Finding {
	var out []analyzer.Finding
	ctx.Explain.Walk(func(n *explain.Node) {
		if n.NodeType != "Seq Scan" {
			return
		}
		if n.Filter == "" {
			return
		}
		scanned := n.ActualRows + float64(n.RowsRemovedByF)
		if scanned < 100000 {
			return // small tables: seq scan is usually correct
		}
		sel := 1.0
		if scanned > 0 {
			sel = n.ActualRows / scanned
		}
		if sel > 0.1 {
			return // filter not selective enough for index to clearly win
		}
		out = append(out, analyzer.Finding{
			Severity: analyzer.SeverityWarn,
			Message: fmt.Sprintf("Seq Scan on %s read %.0f rows and filtered %d away (sel=%.3f) — index likely wins",
				n.RelationName, scanned, n.RowsRemovedByF, sel),
			Explanation: "Seq Scan's cost is proportional to relpages regardless of filter selectivity " +
				"(cost_seqscan in src/backend/optimizer/path/costsize.c:270). With a highly selective filter " +
				"on a large table, a matching index scan reads much less.",
			Suggestion: "Consider CREATE INDEX on the filter column(s). Use a partial index if only a subset is queried.",
			Evidence:   "src/backend/optimizer/path/costsize.c:270; GUIA_POSTGRES_ES_2.md §18.2",
		})
	})
	return out
}

func init() {
	Register(explainEstimatesVsActual{})
	Register(explainExternalSort{})
	Register(explainHashBatches{})
	Register(explainIOSHeapFetches{})
	Register(explainSeqScanLargeTable{})
}

func relOrIndex(n *explain.Node) string {
	if n.RelationName != "" {
		return n.RelationName
	}
	if n.IndexName != "" {
		return n.IndexName
	}
	return "?"
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
