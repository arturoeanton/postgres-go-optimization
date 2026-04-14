# pgopt — PostgreSQL query optimizer & advisor

**[🇪🇸 Leer en español](README.es.md)** | **[📖 Tutorial (en)](docs/TUTORIAL.en.md)** · **[Tutorial paso a paso (es)](docs/TUTORIAL.es.md)**

A 100% CLI tool that analyzes SQL queries and recommends optimizations grounded in the PostgreSQL engine internals (MVCC, planner, executor, indexes, autovacuum). Every finding cites the exact source file and line in the PostgreSQL tree so you can verify each claim.

Built on top of `pg_query_go` — **the real PostgreSQL parser, embedded** — so we see the query exactly the way the server does.

## Highlights

- **50 rules** across AST-only (31), schema-aware (10), and EXPLAIN-based (9) analysis.
- **Source-grounded**: every finding points to `src/backend/...:line` in PostgreSQL.
- **Pipe-friendly**: stdin/stdout, exit codes, text + JSON output.
- **Safe by default**: DB connection runs in `READ ONLY` transaction; no autofix.
- **Well tested**: unit tests per rule, integration fixtures, race-detector clean.
- **Self-contained**: zero external services, just Go + CGO.
- **Batteries included**: `docker compose up -d` gives you a seeded PostgreSQL 17 for instant demos.

## Quickstart

Requirements: Go 1.22+, a C toolchain (pg_query_go embeds PostgreSQL's parser).

```sh
cd go_optimization
go mod tidy
make build          # produces ./pgopt

# Analyze a file
./pgopt query.sql

# From stdin (pipeline-friendly)
echo "SELECT * FROM users WHERE lower(email) = 'a@b.com' LIMIT 10 OFFSET 100000;" | ./pgopt -

# All rules
./pgopt --list-rules

# JSON for CI/pipelines
./pgopt --format json --fail-on warn query.sql
```

### Seeded DB demo (requires Docker)

```sh
# 1. Spin up PostgreSQL 17 with a realistic seeded schema.
make docker-up

# 2. Point pgopt at it.
export PGOPT_DB="postgres://pgopt:pgopt@localhost:55432/pgopt?sslmode=disable"

# 3. Run schema-aware & EXPLAIN-based analysis against real data.
./pgopt --db "$PGOPT_DB" --verbose testdata/demo/missing_idx.sql
./pgopt --db "$PGOPT_DB" --explain --verbose testdata/demo/bad_query_1.sql

# 4. Tear down.
make docker-down
```

The seed (`docker/init.sql` + `docker/seed.sql`) deliberately creates:

- a duplicate index (`duplicate_index`)
- a redundant prefix index (`redundant_index`)
- an FK without a supporting index (`fk_without_index`)
- a table without a primary key (`missing_primary_key`)
- a `timestamp` column without time zone (`timestamp_without_tz`)
- a partitioned `events` table (`partition_key_unused`)
- a big (500k-row) table for `explain_seqscan_large` / `explain_temp_buffers`.

## Example

```
$ ./pgopt --verbose testdata/bad/offset_big.sql

[WARN]  OFFSET 100000: cost is linear in the offset (executor produces and discards 100000 rows)
    [offset_pagination]  at 2:37
    ╰─ OFFSET 100000
    why: PostgreSQL's executor uses the Volcano pull model (src/include/executor/executor.h:
         ExecProcNode). OFFSET N literally calls the child node N times and throws the
         results away. Keyset pagination (WHERE key > :last_seen ORDER BY key LIMIT N)
         seeks directly in the index in O(log n).
    fix: Rewrite as: WHERE <order_col> > :last_value ORDER BY <order_col> LIMIT N.
         Track the last value seen on the client side between pages.
    ref: src/backend/executor/nodeLimit.c (OFFSET handling); GUIA_POSTGRES_ES_2.md §35.2

1 finding(s): 0 error, 1 warn, 0 info
```

## Rules (50 built-in + optional JavaScript)

> The 50 rules below are compiled into the binary and always available.
> Enable `--js-rules` to additionally load user-defined rules from
> `rules-js/` (the repository ships 11 example JS rules). See
> [`docs/js-rules.md`](docs/js-rules.md).

### AST-only — 31 rules (no DB)

| Rule | Detects |
|------|---------|
| `select_star` | `SELECT *` — defeats Index-Only Scans, triggers TOAST detoast. |
| `offset_pagination` | `OFFSET N` with large N. |
| `not_in_null` | `NOT IN (subquery)` — NULL-unsafe; use `NOT EXISTS`. |
| `cast_in_where` | `WHERE col::type = ...` blocks indexes. |
| `function_on_column` | `WHERE lower(col) = ...` etc. blocks indexes. |
| `like_leading_wildcard` | `WHERE col LIKE '%foo%'` — needs pg_trgm + GIN. |
| `large_in_list` | `IN (v1...vN)` with ≥100 items. |
| `missing_where` | `UPDATE`/`DELETE` without `WHERE`. |
| `not_sargable` | `WHERE col + N op X` — arithmetic hides the column. |
| `select_for_update_no_limit` | `FOR UPDATE` without `LIMIT`. |
| `distinct_on_joins` | `DISTINCT` + joins — cardinality smell. |
| `count_star_big` | `COUNT(*)` on big tables — use `pg_class.reltuples`. |
| `order_by_ordinal` | `ORDER BY 1, 2` — fragile. |
| `group_by_ordinal` | `GROUP BY 1` — fragile. |
| `implicit_cross_join` | `FROM a, b` without join predicate = Cartesian. |
| `boolean_equals_true` | `col = true` is redundant. |
| `coalesce_in_where` | `COALESCE(col, …)` blocks indexes. |
| `order_by_random` | `ORDER BY random() LIMIT k` — full scan. |
| `subquery_in_select` | Correlated subquery in SELECT = N+1. |
| `window_empty` | `OVER ()` with no PARTITION BY — probably unintended. |
| `truncate_in_transaction` | `TRUNCATE` mid-transaction holds ACCESS EXCLUSIVE. |
| `interval_on_indexed_column` | `col + interval ...` blocks index usage. |
| `recursive_cte_no_limit` | `WITH RECURSIVE` without termination guard. |
| `union_vs_union_all` | `UNION` may hide `UNION ALL` (cheaper). |
| `insert_no_column_list` | `INSERT … VALUES` without column list — fragile. |
| `delete_correlated_subquery` | `DELETE … WHERE EXISTS` → `DELETE … USING`. |
| `sum_case_when_count_filter` | `SUM(CASE WHEN …)` → `COUNT(*) FILTER`. |
| `having_without_group_by` | `HAVING` without `GROUP BY`. |
| `is_null_in_where` | `IS NULL` hint for rare-NULL columns. |
| `cte_unused` | `WITH` clause declared but not referenced. |
| `vacuum_full_in_script` | `VACUUM FULL` from app code (blocking rewrite). |
| `in_subquery_readability` | `IN (SELECT …)` → `EXISTS` hint. |

### Schema-aware — 10 rules (`--db`)

| Rule | Detects |
|------|---------|
| `missing_index` | WHERE column with no leading btree index. |
| `stale_stats` | Table never ANALYZEd or high dead-tuple ratio. |
| `partition_key_unused` | Partitioned table scanned without partition-key filter. |
| `fk_without_index` | FK column has no supporting index. |
| `redundant_index` | Index's keys are strict prefix of another. |
| `duplicate_index` | Two indexes cover identical columns. |
| `unused_index` | `idx_scan == 0` — candidate for `DROP INDEX`. |
| `missing_primary_key` | Regular table without PK. |
| `timestamp_without_tz` | `timestamp` column instead of `timestamptz`. |

### EXPLAIN-based — 9 rules (`--db --explain`)

| Rule | Detects |
|------|---------|
| `explain_estimate_mismatch` | Planner row estimate off by >10×. |
| `explain_external_sort` | Sort spilled to disk. |
| `explain_hash_batches` | Hash Join partitioned to disk. |
| `explain_ios_heap_fetches` | Index-Only Scan fell back to heap (stale VM). |
| `explain_seqscan_large` | Seq Scan on large table with very selective filter. |
| `explain_nestloop_seq_inner` | Nested Loop with Seq Scan inner + many loops. |
| `explain_parallel_underused` | Fewer workers launched than planned. |
| `explain_temp_buffers` | Node wrote temp files. |
| `explain_cold_cache` | <50% cache hit ratio across plan. |

## CLI reference

```
pgopt [flags] <file.sql | - >

--db URL               PostgreSQL URL (postgres://…) — enables schema+explain rules
--explain              Run EXPLAIN (ANALYZE, BUFFERS) inside a READ ONLY tx
--rules SPEC           'all', 'r1,r2', or 'all,-r3' to exclude
--format FMT           text | json
--min-severity SEV     info | warn | error  (hide anything below)
--fail-on SEV          info | warn | error  (exit non-zero threshold; default warn)
--color auto|always|never
--verbose              Include explanations and source evidence
--quiet                Suppress stdout; exit code only
--list-rules           Print every rule and exit
--js-rules             Enable user-defined JavaScript rules
--js-rules-dir DIR     Directory scanned for JS rules (default: rules-js)
--ignore-file PATH     File listing rule IDs to skip (default: .pgoptignore)
--no-ignore-file       Do not read an ignore file even if one exists
--version              Print the pgopt version and exit
```

### Inline pragmas

You can silence a rule for a specific query without editing
`.pgoptignore` by adding a SQL comment:

```sql
-- pgopt:ignore=select_star
SELECT * FROM intentionally_wide_view;

-- pgopt:ignore-next
SELECT *, calendar.day FROM calendar, regions;   -- intentional cross product
```

`-- pgopt:ignore=a,b,...` applies to the rest of the file;
`-- pgopt:ignore-next` applies to the first non-blank, non-comment
line that follows.

**Exit codes**: 0 clean, 1 findings at/above `--fail-on`, 2 parse error, 3 config/IO error.

### Project-level ignore file

Drop a `.pgoptignore` at the repository root and `pgopt` picks it up
automatically:

```
# .pgoptignore — disable noisy rules for this project
implicit_cross_join
boolean_equals_true
```

One rule ID per line; lines starting with `#` are comments. Point at a
different path with `--ignore-file`, or bypass the mechanism entirely
with `--no-ignore-file`.

## Integration

### Pre-commit hook

```sh
#!/bin/sh
# .git/hooks/pre-commit
for f in $(git diff --cached --name-only --diff-filter=ACMR -- '*.sql'); do
  pgopt --fail-on error "$f" || exit 1
done
```

### CI

```yaml
# GitHub Actions
- run: |
    go install github.com/arturoeanton/postgres-go-optimization/cmd/pgopt@latest
    find . -name '*.sql' -exec pgopt --fail-on warn --format json {} +
```

### AI coding assistants

Feed the JSON output as context — it is small, structured, and every item is grounded in the engine source. Much better than "please optimize this query".

```sh
pgopt --format json --verbose query.sql > feedback.json
```

## Known limitations

A handful of AST rules can false-positive on idioms that are legitimate
in context. These will be tuned before the stable 0.1.0 release:

| Rule | When it over-fires |
|------|--------------------|
| `implicit_cross_join` | Intentional cartesian products (rare but valid, e.g. generating a calendar × regions matrix). |
| `boolean_equals_true` | Code generated by ORMs that always emits `col = TRUE` to keep SQL shape consistent. |
| `is_null_in_where` | Legitimate null checks on sparse columns where `IS NULL` is the fastest path. |

If one of these bites a legitimate query in your project, add the rule
ID to a `.pgoptignore` file at the repository root:

```
# .pgoptignore
implicit_cross_join
boolean_equals_true
```

See the [CLI reference](#cli-reference) section above for the full
ignore-file semantics.

## Architecture

```
cmd/pgopt/          — CLI entry point + CLI tests
internal/
  analyzer/         — AST walker, Finding type, orchestrator, Context
  rules/            — one file per rule; Register() in init()
  schema/           — catalog loader (pg_class, pg_index, pg_stat_user_tables)
  explain/          — EXPLAIN JSON runner + parsed plan walker
  rewriter/         — text-patch engine using pg_query location info
  report/           — text + JSON renderers
testdata/
  bad/              — fixtures that trigger specific rules
  good/             — fixtures that should produce zero findings
integration_test.go — runs every fixture end-to-end
```

### Adding a rule

1. Create `internal/rules/my_rule.go`.
2. Implement `rules.Rule` (ID, Description, DefaultSeverity, RequiresSchema, RequiresExplain, Check).
3. `Register()` in an `init()`.
4. Add `testdata/bad/my_rule.sql` with a `-- expect: my_rule` header.
5. Add cases to `rules_test.go`.

### User-defined rules in JavaScript

Rules can also be written as JS plugins loaded at startup. Useful for
encoding project-specific conventions without forking the binary.

```sh
pgopt --js-rules --js-rules-dir ./rules-js query.sql
```

The repository ships eleven example rules under `rules-js/`; see
[`docs/js-rules.md`](docs/js-rules.md) for the manifest schema, the `pg`
helper API, and a worked example.

## Testing

```sh
make test                    # all tests
go test -race ./...          # with race detector
go test -cover ./...         # with coverage
make fixtures                # exercise each bad fixture visually
```

Current coverage snapshot:
- `report`: 97%
- `analyzer`, `rewriter`: 95%
- `rules`: 90%
- `jsrules`: 86%
- `cmd/pgopt` (CLI): 82%
- `schema`, `explain`: covered by live-DB integration tests only

## Philosophy

- **Don't reinvent the planner.** PostgreSQL's optimizer is 100k+ lines of mature C. This tool catches patterns the optimizer *cannot* unwind because they are baked into the query's structure, and advises on schema/stat choices that feed the optimizer good input.
- **No autofix by default.** Every finding has a concrete suggestion but the human decides when to rewrite. A future `--fix` may land for the subset of truly semantically-invariant transformations.
- **Every claim cites the source.** If `pgopt` disagrees with the PostgreSQL code, **the code wins** — open a PR to fix the rule.

## License

BSD-style, matching the PostgreSQL source referenced throughout.
