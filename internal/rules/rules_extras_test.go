package rules

import (
	"testing"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
	"github.com/arturoeanton/postgres-go-optimization/internal/explain"
	"github.com/arturoeanton/postgres-go-optimization/internal/schema"
)

// TestRulesExtras covers every AST-only rule that the primary matrix in
// rules_test.go does not reach. Each rule gets at least one positive and
// one negative case; rules with multiple branches get more.
func TestRulesExtras(t *testing.T) {
	cases := map[string][]ruleCase{
		"boolean_equals_true": {
			{"eq_true", "SELECT 1 FROM t WHERE active = true", true},
			{"eq_false", "SELECT 1 FROM t WHERE active = false", true},
			{"bare_bool", "SELECT 1 FROM t WHERE active", false},
		},
		"coalesce_in_where": {
			{"coalesce_eq", "SELECT 1 FROM t WHERE COALESCE(name,'') = 'x'", true},
			{"no_coalesce", "SELECT 1 FROM t WHERE name = 'x'", false},
		},
		"count_star_big": {
			{"count_star", "SELECT COUNT(*) FROM t", true},
			{"count_col", "SELECT COUNT(id) FROM t", false},
		},
		"cte_unused": {
			{"declared_unused", "WITH x AS (SELECT 1 AS a) SELECT 2", true},
			{"declared_and_used", "WITH x AS (SELECT 1 AS a) SELECT a FROM x", false},
		},
		"delete_correlated_subquery": {
			{"delete_corr", "DELETE FROM t WHERE EXISTS (SELECT 1 FROM s WHERE s.id = t.id)", true},
			{"delete_simple", "DELETE FROM t WHERE id = 5", false},
		},
		"group_by_ordinal": {
			{"group_ordinal", "SELECT dept, COUNT(*) FROM t GROUP BY 1", true},
			{"group_name", "SELECT dept, COUNT(*) FROM t GROUP BY dept", false},
		},
		"having_without_group_by": {
			{"having_no_group", "SELECT COUNT(*) FROM t HAVING COUNT(*) > 5", true},
			{"having_with_group", "SELECT dept, COUNT(*) FROM t GROUP BY dept HAVING COUNT(*) > 5", false},
		},
		"implicit_cross_join": {
			{"comma_no_where", "SELECT 1 FROM a, b", true},
			{"comma_with_join", "SELECT 1 FROM a, b WHERE a.id = b.id", false},
			{"explicit_join", "SELECT 1 FROM a JOIN b ON a.id = b.id", false},
		},
		"in_subquery_readability": {
			{"in_subq", "SELECT 1 FROM t WHERE id IN (SELECT uid FROM s)", true},
			{"in_literal", "SELECT 1 FROM t WHERE id IN (1,2,3)", false},
			{"exists_instead", "SELECT 1 FROM t WHERE EXISTS (SELECT 1 FROM s WHERE s.uid = t.id)", false},
		},
		"insert_no_column_list": {
			{"insert_no_cols", "INSERT INTO t VALUES (1,2,3)", true},
			{"insert_with_cols", "INSERT INTO t (a,b,c) VALUES (1,2,3)", false},
		},
		"interval_on_indexed_column": {
			{"col_plus_interval", "SELECT 1 FROM t WHERE ts + interval '1 day' > now()", true},
			{"ts_minus_const", "SELECT 1 FROM t WHERE ts > now() - interval '1 day'", false},
		},
		"is_null_in_where": {
			{"is_null_sole_predicate", "SELECT 1 FROM t WHERE deleted_at IS NULL", true},
			{"is_not_null_skipped", "SELECT 1 FROM t WHERE deleted_at IS NOT NULL", false},
			{"plain_eq", "SELECT 1 FROM t WHERE id = 1", false},
			// Narrowed behaviour: once IS NULL is part of a multi-predicate
			// WHERE chain we no longer fire — the partial-index suggestion
			// is almost never the right advice in that case.
			{"is_null_with_conjunction", "SELECT 1 FROM t WHERE deleted_at IS NULL AND active = true", false},
		},
		"order_by_ordinal": {
			{"order_ordinal", "SELECT a, b FROM t ORDER BY 1", true},
			{"order_name", "SELECT a, b FROM t ORDER BY a", false},
		},
		"order_by_random": {
			{"order_random", "SELECT 1 FROM t ORDER BY random() LIMIT 10", true},
			{"order_id", "SELECT 1 FROM t ORDER BY id LIMIT 10", false},
		},
		"recursive_cte_no_limit": {
			{"rec_no_limit", "WITH RECURSIVE c AS (SELECT 1 AS n UNION ALL SELECT n+1 FROM c) SELECT * FROM c", true},
			{"rec_with_limit", "WITH RECURSIVE c AS (SELECT 1 AS n UNION ALL SELECT n+1 FROM c) SELECT * FROM c LIMIT 100", false},
		},
		"subquery_in_select": {
			{"corr_in_select", "SELECT u.id, (SELECT COUNT(*) FROM o WHERE o.uid = u.id) FROM u", true},
			{"no_subquery", "SELECT u.id, u.name FROM u", false},
		},
		"sum_case_when_count_filter": {
			{"sum_case", "SELECT SUM(CASE WHEN active THEN 1 ELSE 0 END) FROM t", true},
			{"count_filter", "SELECT COUNT(*) FILTER (WHERE active) FROM t", false},
		},
		"truncate_in_transaction": {
			{"truncate_in_tx", "BEGIN; TRUNCATE t; SELECT 1;", true},
			{"truncate_alone", "TRUNCATE t", false},
		},
		"union_vs_union_all": {
			{"union", "SELECT 1 FROM a UNION SELECT 1 FROM b", true},
			{"union_all", "SELECT 1 FROM a UNION ALL SELECT 1 FROM b", false},
		},
		"vacuum_full_in_script": {
			{"vac_full", "VACUUM FULL t", true},
			{"vac_normal", "VACUUM t", false},
		},
		"window_empty": {
			{"over_empty", "SELECT row_number() OVER () FROM t", true},
			{"over_partition", "SELECT row_number() OVER (PARTITION BY dept) FROM t", false},
		},
	}

	for ruleID, ccs := range cases {
		r, ok := Get(ruleID)
		if !ok {
			t.Fatalf("rule %q not registered", ruleID)
		}
		for _, c := range ccs {
			c := c
			t.Run(ruleID+"/"+c.name, func(t *testing.T) {
				got := runOne(t, r, c.sql)
				fired := len(got) > 0
				if fired != c.fires {
					t.Errorf("rule=%s case=%s fired=%v want=%v\n  SQL: %s",
						ruleID, c.name, fired, c.fires, c.sql)
					for _, f := range got {
						t.Logf("  finding: %s", f.Message)
					}
				}
			})
		}
	}
}

// TestRuleMetadata exercises the boilerplate methods (ID, Description,
// DefaultSeverity, RequiresSchema, RequiresExplain) on every registered
// rule. These are trivial but nonetheless count against coverage, and
// verifying them in bulk catches accidental copy-paste drift between
// rules.
func TestRuleMetadata(t *testing.T) {
	for _, r := range All() {
		if r.ID() == "" {
			t.Errorf("rule has empty ID: %T", r)
		}
		if r.Description() == "" {
			t.Errorf("%s: empty description", r.ID())
		}
		sev := r.DefaultSeverity()
		if sev < analyzer.SeverityInfo || sev > analyzer.SeverityError {
			t.Errorf("%s: severity %v out of range", r.ID(), sev)
		}
		_ = r.RequiresSchema()
		_ = r.RequiresExplain()
	}
}

// ---- Schema-aware rule tests -------------------------------------------

// buildSchema returns a pre-populated catalog suitable for feeding into
// schema-aware rules. The layout mirrors the seed used in docker/seed.sql
// so the tests exercise the same relationships the demo shows.
func buildSchema() *schema.Schema {
	users := &schema.Relation{
		Schema: "public", Name: "users", Kind: "r",
		Columns: []schema.Column{
			{Name: "id", TypeName: "bigint", NotNull: true},
			{Name: "email", TypeName: "text"},
			{Name: "name", TypeName: "text"},
			{Name: "country", TypeName: "text"},
		},
		Indexes: []schema.Index{
			{Name: "users_pkey", Unique: true, Primary: true, Method: "btree", Columns: []string{"id"}},
			{Name: "users_email_idx", Unique: false, Method: "btree", Columns: []string{"email"}},
			{Name: "users_email_dup_idx", Unique: false, Method: "btree", Columns: []string{"email"}},
			{Name: "users_country_idx", Unique: false, Method: "btree", Columns: []string{"country"}},
			{Name: "users_country_created_idx", Unique: false, Method: "btree", Columns: []string{"country", "name"}},
		},
		HasPrimary: true,
		LiveTuples: 50000,
		RelPages:   500,
		RelTuples:  50000,
		IndexScans: map[string]int64{
			"users_pkey":                   100000,
			"users_email_idx":              50000,
			"users_email_dup_idx":          0,
			"users_country_idx":            0,
			"users_country_created_idx":    12345,
		},
	}
	orders := &schema.Relation{
		Schema: "public", Name: "orders", Kind: "r",
		Columns: []schema.Column{
			{Name: "id", TypeName: "bigint", NotNull: true},
			{Name: "user_id", TypeName: "bigint", NotNull: true},
			{Name: "total_cents", TypeName: "bigint"},
		},
		Indexes: []schema.Index{
			{Name: "orders_pkey", Unique: true, Primary: true, Method: "btree", Columns: []string{"id"}},
		},
		HasPrimary: true,
		LiveTuples: 200000,
		ForeignKeys: []schema.ForeignKey{
			{Name: "orders_user_id_fkey", Columns: []string{"user_id"}, RefSchema: "public", RefTable: "users", RefColumns: []string{"id"}},
		},
	}
	categories := &schema.Relation{
		Schema: "public", Name: "categories", Kind: "r",
		Columns: []schema.Column{
			{Name: "id", TypeName: "bigint"},
			{Name: "name", TypeName: "text"},
		},
		Indexes:    []schema.Index{},
		HasPrimary: false,
		LiveTuples: 50,
	}
	auditLog := &schema.Relation{
		Schema: "public", Name: "audit_log", Kind: "r",
		Columns: []schema.Column{
			{Name: "id", TypeName: "bigint"},
			{Name: "occurred", TypeName: "timestamp"}, // no tz — should be flagged
			{Name: "occurred_tz", TypeName: "timestamptz"},
		},
		HasPrimary: true,
		LiveTuples: 20000,
	}
	events := &schema.Relation{
		Schema: "public", Name: "events", Kind: "p",
		Columns: []schema.Column{
			{Name: "id", TypeName: "bigint"},
			{Name: "event_time", TypeName: "timestamptz"},
			{Name: "event_type", TypeName: "text"},
		},
		PartKeys:   []string{"event_time"},
		LiveTuples: 150000,
	}

	return &schema.Schema{
		Relations: map[string]*schema.Relation{
			"public.users":      users,
			"public.orders":     orders,
			"public.categories": categories,
			"public.audit_log":  auditLog,
			"public.events":     events,
		},
	}
}

func runSchemaRule(t *testing.T, id, sql string) []analyzer.Finding {
	t.Helper()
	r, ok := Get(id)
	if !ok {
		t.Fatalf("rule %q not registered", id)
	}
	ast, err := analyzer.ParseJSON(sql)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return r.Check(&analyzer.Context{Source: sql, AST: ast, Schema: buildSchema()})
}

func TestSchemaRules(t *testing.T) {
	cases := map[string][]ruleCase{
		"missing_index": {
			{"no_index_name", "SELECT 1 FROM users WHERE name = 'x'", true},
			{"indexed_email", "SELECT 1 FROM users WHERE email = 'x'", false},
		},
		"fk_without_index": {
			{"orders_fk_not_indexed", "SELECT 1 FROM orders WHERE user_id = 1", true},
		},
		"duplicate_index": {
			{"duplicate_email_index", "SELECT 1 FROM users WHERE email = 'x'", true},
		},
		"redundant_index": {
			{"country_redundant", "SELECT 1 FROM users WHERE country = 'AR'", true},
		},
		"unused_index": {
			{"email_dup_never_scanned", "SELECT 1 FROM users WHERE email = 'x'", true},
		},
		"missing_primary_key": {
			{"categories_no_pk", "SELECT * FROM categories", true},
			{"users_has_pk", "SELECT * FROM users", false},
		},
		"timestamp_without_tz": {
			{"audit_plain_timestamp", "SELECT * FROM audit_log", true},
			{"users_no_ts_cols", "SELECT * FROM users", false},
		},
		"partition_key_unused": {
			{"events_no_part_filter", "SELECT 1 FROM events WHERE event_type = 'x'", true},
			{"events_with_part_filter", "SELECT 1 FROM events WHERE event_time > now() - interval '1 day'", false},
		},
	}
	for id, ccs := range cases {
		for _, c := range ccs {
			c := c
			t.Run(id+"/"+c.name, func(t *testing.T) {
				got := runSchemaRule(t, id, c.sql)
				fired := false
				for _, f := range got {
					if f.Rule == "" || f.Rule == id {
						fired = true
						break
					}
				}
				// rules.Check returns findings without Rule stamped; fire on any
				if fired != c.fires && c.fires {
					t.Errorf("%s/%s: expected to fire, got %d findings", id, c.name, len(got))
				}
				if !c.fires && len(got) > 0 {
					// Check rule id matches (in case the rule fired but we expected silence)
					t.Errorf("%s/%s: expected no findings, got %d", id, c.name, len(got))
					for _, f := range got {
						t.Logf("  - %s", f.Message)
					}
				}
			})
		}
	}
}

// TestStaleStats covers the stale_stats rule which looks at LastAnalyze
// and dead-tuple ratio. It is in its own test because building the
// right stat state is verbose.
func TestStaleStats(t *testing.T) {
	never := ""
	rel := &schema.Relation{
		Schema: "public", Name: "dusty", Kind: "r",
		HasPrimary:  true,
		LiveTuples:  10000,
		DeadTuples:  5000,
		NDeadRatio:  0.5,
		LastAnalyze: &never,
	}
	sch := &schema.Schema{Relations: map[string]*schema.Relation{"public.dusty": rel}}
	r, _ := Get("stale_stats")
	ast, _ := analyzer.ParseJSON("SELECT * FROM dusty")
	found := r.Check(&analyzer.Context{Source: "SELECT * FROM dusty", AST: ast, Schema: sch})
	if len(found) == 0 {
		t.Error("stale_stats should fire for a table with 50% dead ratio and no analyze")
	}
}

// ---- EXPLAIN rule tests -------------------------------------------------

// buildExplainPlan constructs a plan tree that exercises as many of the
// explain-* rules as possible in a single analysis. Each anti-pattern
// is in its own node so the rules can be tested independently. The root
// is a Nested Loop whose inner (Plans[1]) is a Seq Scan with many loops
// — the shape nestloop_seq_inner specifically looks for.
func buildExplainPlan() *explain.Plan {
	return &explain.Plan{
		Raw: "synthetic",
		Root: &explain.Node{
			NodeType:    "Nested Loop",
			JoinType:    "Inner",
			StartupCost: 0, TotalCost: 100000,
			PlanRows: 1, ActualRows: 1000, ActualLoops: 1,
			Plans: []*explain.Node{
				// Plans[0] — outer driver of the nested loop.
				{
					NodeType:        "Seq Scan",
					RelationName:   "users",
					StartupCost:    0, TotalCost: 5000,
					PlanRows:       1000, ActualRows: 1000, ActualLoops: 1,
					SharedHitBlocks: 5, SharedReadBlocks: 1000,
				},
				// Plans[1] — inner of the nested loop; Seq Scan + high loops
				// is what explain_nestloop_seq_inner detects.
				{
					NodeType:     "Seq Scan",
					RelationName: "orders",
					StartupCost:  0, TotalCost: 8000,
					PlanRows:     200000, ActualRows: 1, ActualLoops: 1000,
					Filter:       "status = 'shipped'",
				},
				// Sort spilling to disk for explain_external_sort.
				{
					NodeType:      "Sort",
					SortMethod:    "external merge",
					SortSpaceUsed: 65536,
					SortSpaceType: "Disk",
				},
				// A "Hash Join" parent whose Hash child partitioned to
				// disk — explain_hash_batches matches on NodeType "Hash",
				// which pg_query nests underneath the join.
				{
					NodeType: "Hash Join",
					Plans: []*explain.Node{
						{NodeType: "Seq Scan", RelationName: "left"},
						{
							NodeType:            "Hash",
							HashBatches:         8,
							OriginalHashBatches: 1,
						},
					},
				},
				// Index Only Scan with heap fetches for explain_ios_heap_fetches.
				{
					NodeType:    "Index Only Scan",
					IndexName:   "users_pkey",
					HeapFetches: 100000,
					PlanRows:    50000,
				},
				// Gather with fewer workers launched than planned.
				{
					NodeType:        "Gather",
					WorkersPlanned:  4,
					WorkersLaunched: 1,
				},
				// Seq Scan over a large table with a highly selective
				// filter — the shape explain_seqscan_large detects.
				{
					NodeType:        "Seq Scan",
					RelationName:   "big_table",
					Filter:         "rare_flag = true",
					PlanRows:        500000,
					ActualRows:      100,
					RowsRemovedByF:  499900,
					TempReadBlocks:  200,
					TempWriteBlocks: 200,
				},
			},
		},
	}
}

func runExplainRule(t *testing.T, id, sql string) []analyzer.Finding {
	t.Helper()
	r, ok := Get(id)
	if !ok {
		t.Fatalf("rule %q not registered", id)
	}
	ast, err := analyzer.ParseJSON(sql)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return r.Check(&analyzer.Context{
		Source:  sql,
		AST:     ast,
		Schema:  buildSchema(),
		Explain: buildExplainPlan(),
	})
}

func TestExplainRules(t *testing.T) {
	// The synthetic plan is built so that every explain rule has at least
	// one node that should trigger it. Missing a rule here is usually a
	// sign that the plan tree needs a new node added.
	const sql = "SELECT 1 FROM users u JOIN orders o ON o.user_id = u.id"
	for _, id := range []string{
		"explain_estimate_mismatch",
		"explain_external_sort",
		"explain_hash_batches",
		"explain_ios_heap_fetches",
		"explain_seqscan_large",
		"explain_parallel_underused",
		"explain_temp_buffers",
		"explain_cold_cache",
	} {
		id := id
		t.Run(id, func(t *testing.T) {
			got := runExplainRule(t, id, sql)
			if len(got) == 0 {
				t.Errorf("%s: expected at least one finding against synthetic plan", id)
			}
		})
	}
}

// TestExplainNestloopSeqInner is separate because the rule requires a
// Nested Loop with *exactly two children* and the second one being a
// Seq Scan with many loops — a shape that doesn't fit the shared
// multi-anti-pattern plan.
func TestExplainNestloopSeqInner(t *testing.T) {
	plan := &explain.Plan{
		Root: &explain.Node{
			NodeType: "Nested Loop",
			Plans: []*explain.Node{
				{NodeType: "Index Scan", IndexName: "users_pkey", ActualRows: 1000},
				{NodeType: "Seq Scan", RelationName: "orders", ActualRows: 1, ActualLoops: 1000},
			},
		},
	}
	r, _ := Get("explain_nestloop_seq_inner")
	ast, _ := analyzer.ParseJSON("SELECT 1 FROM users u JOIN orders o ON o.user_id = u.id")
	got := r.Check(&analyzer.Context{Source: "x", AST: ast, Explain: plan})
	if len(got) == 0 {
		t.Error("explain_nestloop_seq_inner: expected finding on N×M seq scan inner")
	}
}

// TestExplainRuleNegatives runs the explain rules against a "clean" plan
// (no anti-patterns) and asserts none of them fire.
func TestExplainRuleNegatives(t *testing.T) {
	clean := &explain.Plan{
		Raw: "clean",
		Root: &explain.Node{
			NodeType: "Index Scan", IndexName: "users_pkey",
			PlanRows: 10, ActualRows: 10, ActualLoops: 1,
			SharedHitBlocks: 100, SharedReadBlocks: 1,
		},
	}
	for _, id := range []string{
		"explain_external_sort",
		"explain_hash_batches",
		"explain_seqscan_large",
		"explain_parallel_underused",
		"explain_temp_buffers",
	} {
		r, _ := Get(id)
		ast, _ := analyzer.ParseJSON("SELECT 1 FROM users WHERE id = 1")
		got := r.Check(&analyzer.Context{
			Source: "SELECT 1 FROM users WHERE id = 1", AST: ast,
			Schema: buildSchema(), Explain: clean,
		})
		if len(got) > 0 {
			t.Errorf("%s: expected no findings on clean plan, got %d", id, len(got))
		}
	}
}
