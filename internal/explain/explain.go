// Package explain runs EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) on a live
// database and parses the JSON plan into a walkable tree.
//
// We use the ANALYZE variant because only actual-vs-estimate comparisons
// tell us whether stats are accurate. Callers who cannot tolerate the
// side effects of running the query should set --dry-run (not implemented
// here; caller responsibility).
package explain

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Plan is the root of a parsed EXPLAIN (FORMAT JSON) output.
type Plan struct {
	Raw  string
	Root *Node
}

// Node is one node in the plan tree. Field names match EXPLAIN output.
type Node struct {
	NodeType         string    `json:"Node Type"`
	RelationName     string    `json:"Relation Name,omitempty"`
	IndexName        string    `json:"Index Name,omitempty"`
	Alias            string    `json:"Alias,omitempty"`
	StartupCost      float64   `json:"Startup Cost"`
	TotalCost        float64   `json:"Total Cost"`
	PlanRows         float64   `json:"Plan Rows"`
	PlanWidth        int       `json:"Plan Width"`
	ActualRows       float64   `json:"Actual Rows,omitempty"`
	ActualLoops      float64   `json:"Actual Loops,omitempty"`
	ActualStartup    float64   `json:"Actual Startup Time,omitempty"`
	ActualTotal      float64   `json:"Actual Total Time,omitempty"`
	Filter           string    `json:"Filter,omitempty"`
	RowsRemovedByF   int64     `json:"Rows Removed by Filter,omitempty"`
	IndexCond        string    `json:"Index Cond,omitempty"`
	HashCond         string    `json:"Hash Cond,omitempty"`
	MergeCond        string    `json:"Merge Cond,omitempty"`
	JoinType         string    `json:"Join Type,omitempty"`
	SortMethod       string    `json:"Sort Method,omitempty"`
	SortSpaceUsed    int64     `json:"Sort Space Used,omitempty"`
	SortSpaceType    string    `json:"Sort Space Type,omitempty"`
	HashBuckets      int64     `json:"Hash Buckets,omitempty"`
	OriginalHashBatches int64  `json:"Original Hash Batches,omitempty"`
	HashBatches      int64     `json:"Hash Batches,omitempty"`
	PeakMemoryKB     int64     `json:"Peak Memory Usage,omitempty"`
	HeapFetches      int64     `json:"Heap Fetches,omitempty"`
	SharedHitBlocks  int64     `json:"Shared Hit Blocks,omitempty"`
	SharedReadBlocks int64     `json:"Shared Read Blocks,omitempty"`
	TempReadBlocks   int64     `json:"Temp Read Blocks,omitempty"`
	TempWriteBlocks  int64     `json:"Temp Written Blocks,omitempty"`
	WorkersPlanned   int       `json:"Workers Planned,omitempty"`
	WorkersLaunched int        `json:"Workers Launched,omitempty"`
	Parallel         bool      `json:"Parallel Aware,omitempty"`
	Plans            []*Node   `json:"Plans,omitempty"`
}

// rawPlan is the top-level structure EXPLAIN emits: [ {"Plan": { ... }} ].
type rawPlan struct {
	Plan Node `json:"Plan"`
}

// Run opens a short connection, executes EXPLAIN, and returns the parsed plan.
func Run(ctx context.Context, url, sql string) (*Plan, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse db url: %w", err)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	// Wrap the user query in a READ ONLY transaction; EXPLAIN ANALYZE with
	// DML (INSERT/UPDATE/DELETE) would execute it for real otherwise. A
	// read-only tx rejects writes even inside EXPLAIN ANALYZE.
	if _, err := conn.Exec(ctx, "BEGIN READ ONLY"); err != nil {
		return nil, err
	}
	defer conn.Exec(ctx, "ROLLBACK") //nolint:errcheck

	query := "EXPLAIN (ANALYZE true, BUFFERS true, VERBOSE false, FORMAT JSON) " + sql
	var raw string
	row := conn.QueryRow(ctx, query)
	if err := row.Scan(&raw); err != nil {
		return nil, fmt.Errorf("EXPLAIN failed: %w", err)
	}

	var parsed []rawPlan
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("parse EXPLAIN JSON: %w", err)
	}
	if len(parsed) == 0 {
		return nil, fmt.Errorf("EXPLAIN returned no plans")
	}
	p := &Plan{Raw: raw, Root: &parsed[0].Plan}
	return p, nil
}

// Walk performs a pre-order traversal of the plan.
func (p *Plan) Walk(visit func(*Node)) {
	if p == nil || p.Root == nil {
		return
	}
	walkNode(p.Root, visit)
}

func walkNode(n *Node, visit func(*Node)) {
	visit(n)
	for _, c := range n.Plans {
		walkNode(c, visit)
	}
}
