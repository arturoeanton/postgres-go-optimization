# Step-by-step tutorial: using pgopt to optimize PostgreSQL queries

> This tutorial assumes you have already built the binary (`make build` from the repo root or `go install ./cmd/pgopt`). If `./pgopt` is ready, let's start.

The goal: learn to use `pgopt` as a daily tool — from prototyping in psql to a CI linter that blocks PRs with risky queries.

**What we cover**: the 50 built-in rules grouped in 3 layers (AST / schema / EXPLAIN), the optional **user-defined JavaScript rules** layer, how to spin up the demo database with Docker, how to add new rules (Go or JS), how to wire it into CI, and how to feed context to an assistant that writes SQL.

---

## Lesson 1 — Hello world

Start with an obviously bad query:

```sh
echo "SELECT * FROM users WHERE lower(email) = 'alice@example.com';" | ./pgopt -
```

Output (trimmed):

```
[WARN]  SELECT * pulls every column, defeating Index-Only Scans ...  [select_star]  at 1:8
[WARN]  Function `lower(email)` in WHERE disables plain indexes on `email`  [function_on_column]  at 1:28

2 finding(s): 0 error, 2 warn, 0 info
```

Two warnings. Exit code is 1 because we have warnings and `--fail-on` defaults to `warn`:

```sh
echo "SELECT * FROM t" | ./pgopt -
echo "Exit: $?"
# Exit: 1
```

### What just happened

1. `pgopt` read the SQL from stdin.
2. It parsed it with the real PostgreSQL parser (via `pg_query_go`).
3. It walked the AST applying 11 AST-only rules.
4. It reported two findings.

It did not connect to any database, did not gather statistics, did not execute anything. Pure syntactic analysis plus engine knowledge.

---

## Lesson 2 — Verbose mode and the "why"

```sh
echo "SELECT * FROM t" | ./pgopt --verbose --color=always -
```

Each finding now shows three blocks:
- **`why:`** the engine explanation. Why is this bad? It cites `src/backend/parser/parse_target.c` so you can open the file and verify.
- **`fix:`** the concrete rewrite suggestion.
- **`ref:`** the canonical reference (file:line) for deeper reading.

This is the mode you hand to a human who is learning, or to a language-model assistant so it understands the context before rewriting.

---

## Lesson 3 — File input

```sh
cat > /tmp/q.sql <<'SQL'
SELECT id, email
  FROM users
 WHERE created_at::date = '2026-01-01'
 ORDER BY id
 LIMIT 50
 OFFSET 100000;
SQL

./pgopt --verbose /tmp/q.sql
```

Two findings:
- `cast_in_where` on line 3 (the `::date` cast breaks the index).
- `offset_pagination` on line 6 (large offset).

**Every finding carries a `line:col` position** — click in your editor to jump there.

---

## Lesson 4 — Selecting which rules to run

Three forms:

```sh
# All (default)
./pgopt query.sql

# Specific subset
./pgopt --rules "select_star,offset_pagination" query.sql

# All except one (useful when a rule false-positives in your context)
./pgopt --rules "all,-distinct_on_joins" query.sql
```

List what is available:

```sh
./pgopt --list-rules
```

Rules tagged `[schema]` or `[explain]` only run with `--db`. Rules tagged `[js]` only appear when you add `--js-rules` (see Lesson 12).

---

## Lesson 5 — Severity control and exit codes

Three levels: `info`, `warn`, `error`.

```sh
# Show only errors (hide warns and infos)
./pgopt --min-severity error query.sql

# Do not fail on warns — only on errors (useful if you want CI to block only clear escapes)
./pgopt --fail-on error query.sql

# Show everything but never fail (informative only)
./pgopt --min-severity info --fail-on error query.sql
```

Exit codes:

| Code | Meaning |
|------|---------|
| 0 | No findings at or above `--fail-on` |
| 1 | Findings at or above `--fail-on` |
| 2 | SQL does not parse (invalid syntax) |
| 3 | Configuration or I/O error |

---

## Lesson 6 — JSON output for scripts

```sh
./pgopt --format json query.sql
```

```json
{
  "findings": [
    {
      "rule": "select_star",
      "severity": 1,
      "message": "SELECT * pulls every column...",
      "explanation": "Expansion happens during parse_analyze...",
      "suggestion": "List only the columns you actually need...",
      "evidence": "src/backend/parser/parse_target.c:ExpandColumnRefStar",
      "location": {"start": 7, "end": 8},
      "snippet": "*"
    }
  ],
  "summary": { "total": 1, "error": 0, "warn": 1, "info": 0 }
}
```

With this you can:

```sh
# Count warnings across your migrations folder
find migrations -name '*.sql' -exec ./pgopt --format json --quiet {} \; \
  | jq -s '[.[] | .summary.warn] | add'

# Filter by a single rule
./pgopt --format json query.sql | jq '.findings[] | select(.rule=="offset_pagination")'
```

---

## Lesson 7 — Connecting to the database (schema-aware): Docker in 30 seconds

Schema-aware rules need `pgopt` to read the catalog (`pg_class`, `pg_index`, `pg_stat_user_tables`, `pg_constraint`, etc.). A **Docker stack is included** that spins up PostgreSQL 17 with a schema seeded intentionally with anti-patterns.

### Start the demo DB

```sh
make docker-up
# ✓ PostgreSQL ready on localhost:55432

export PGOPT_DB="postgres://pgopt:pgopt@localhost:55432/pgopt?sslmode=disable"
```

The seed (`docker/init.sql` + `docker/seed.sql`) creates:

| Table | Size | Purpose |
|-------|------|---------|
| `users` | 50k rows | Duplicate index, redundant index, column without index. |
| `orders` | 200k rows | FK to `users.id` with NO supporting index. |
| `categories` | 50 rows | No primary key. |
| `audit_log` | 20k rows | `occurred timestamp` (without time zone). |
| `events` | 150k rows | Range-partitioned by date. |
| `big_table` | 500k rows | Large `jsonb` payload (TOAST), for EXPLAIN tests. |

### 10 schema-aware rules triggered against this DB

```sh
echo "SELECT * FROM users u JOIN orders o ON o.user_id = u.id WHERE u.name = 'x';" \
  | ./pgopt --db "$PGOPT_DB" -
```

Actual output:

```
[WARN]  Duplicate indexes on users: `users_email_idx` and `users_email_dup_idx` cover identical columns
        [duplicate_index]
[WARN]  Foreign key `public.orders(user_id)` has no supporting index  [fk_without_index]
[WARN]  No index on `public.users(name)` for predicate `=`  [missing_index]
[INFO]  Index `users_country_idx` on users is a strict prefix of `users_country_created_idx`
        and is likely redundant  [redundant_index]
```

You can check each rule in isolation:

```sh
echo "SELECT * FROM categories;" | ./pgopt --db "$PGOPT_DB" --rules missing_primary_key -
echo "SELECT * FROM audit_log;" | ./pgopt --db "$PGOPT_DB" --rules timestamp_without_tz -
./pgopt --db "$PGOPT_DB" --rules partition_key_unused testdata/demo/partition_unused.sql
echo "SELECT * FROM big_table LIMIT 1;" | ./pgopt --db "$PGOPT_DB" --rules unused_index -
```

**Safety**: the connection is put into `SET default_transaction_read_only = on` as soon as it is opened. `pgopt` cannot modify your DB, **not even with `--explain`** (which runs inside `BEGIN READ ONLY`).

### Tear down the stack

```sh
make docker-down
```

---

## Lesson 8 — Integrated EXPLAIN: 9 rules over the real plan

```sh
./pgopt --db "$PGOPT_DB" --explain --verbose testdata/demo/bad_query_1.sql
```

This:

1. Connects to the DB.
2. Opens a `READ ONLY` transaction (protection: an `EXPLAIN ANALYZE` over an `INSERT`/`UPDATE`/`DELETE` does NOT execute the mutation).
3. Runs `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)`.
4. `ROLLBACK`.
5. Parses the JSON plan.
6. Applies **9 rules** based on the real plan.

What it detects:

| Rule | Operational hint |
|------|------------------|
| `explain_estimate_mismatch` | Estimated vs actual `rows` differ by >10× → `ANALYZE`, `SET STATISTICS`, `CREATE STATISTICS`. |
| `explain_external_sort` | Sort spilled to disk → `SET LOCAL work_mem`. |
| `explain_hash_batches` | Hash Join partitioned to disk → same remedy. |
| `explain_ios_heap_fetches` | Index Only Scan fell back to the heap (stale VM) → `VACUUM`. |
| `explain_seqscan_large` | Seq Scan over a large table with a very selective filter → index candidate. |
| `explain_nestloop_seq_inner` | Nested Loop with inner Seq Scan and many loops → index on inner or force hash/merge. |
| `explain_parallel_underused` | Fewer workers launched than planned → raise `max_parallel_workers`. |
| `explain_temp_buffers` | A node wrote temp files → `SET LOCAL work_mem`. |
| `explain_cold_cache` | <50% cache hit → cold start or small `shared_buffers`. |

This mode is **expensive** (it executes the query). Use it in CI only for critical queries, or by hand when you are investigating performance.

### Demo against the seeded DB

With `make docker-up` running:

```sh
./pgopt --db "$PGOPT_DB" --explain --verbose testdata/demo/bad_query_1.sql
```

The demo query mixes 5 structural anti-patterns and, over the seeded data, produces an expensive nested loop plus plan findings. You'll see ~10 findings from the 3 layers at once — a good example of why it's worth running pgopt in full mode.

---

## Lesson 9 — Integration with humans and SQL-writing assistants

If you are helping a developer or a language model that writes SQL, the pattern is:

```sh
pgopt --format json --verbose query.sql > feedback.json
```

The JSON carries:
- The problem (message)
- **Why** it is a problem (explanation with a code citation)
- **What to do** (suggestion)

Hand that JSON to the model or developer as context. Rewrites come out much better than with a bare "please optimize this query" prompt.

### Typical workflow

1. Dev writes a query.
2. `pgopt --format json query.sql` produces feedback.
3. The assistant receives `query.sql` + `feedback.json` and proposes an optimized version.
4. Dev runs `pgopt` against the new version — ideally no findings.
5. App tests pass — merge.

---

## Lesson 10 — CI/CD integration

### GitHub Actions

```yaml
name: SQL lint

on: [pull_request]

jobs:
  pgopt:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.22' }
      - run: go install github.com/arturoeanton/postgres-go-optimization/cmd/pgopt@latest
      - name: Lint SQL
        run: |
          changed=$(git diff --name-only origin/${{ github.base_ref }}..HEAD -- '*.sql')
          for f in $changed; do
            echo "::group::$f"
            pgopt --fail-on warn --verbose "$f"
            echo "::endgroup::"
          done
```

### GitLab CI

```yaml
pgopt:
  image: golang:1.22
  script:
    - go install github.com/arturoeanton/postgres-go-optimization/cmd/pgopt@latest
    - find . -name '*.sql' -exec pgopt --fail-on warn {} +
```

### Local pre-commit hook

```sh
cat > .git/hooks/pre-commit <<'EOF'
#!/bin/sh
for f in $(git diff --cached --name-only --diff-filter=ACMR -- '*.sql'); do
  pgopt --fail-on error "$f" || exit 1
done
EOF
chmod +x .git/hooks/pre-commit
```

---

## Lesson 11 — Adding a rule in Go

Scenario: you want to detect `WHERE col = ANY(ARRAY[...])` with huge arrays, because your shop noticed that pattern produces bad plans.

### Step 1 — Write the rule

Create `internal/rules/my_array_any.go`:

```go
package rules

import (
    "fmt"
    "github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

type largeArrayAny struct{}

func (largeArrayAny) ID() string                         { return "large_array_any" }
func (largeArrayAny) Description() string                { return "= ANY(ARRAY[…]) with a huge array: prefer a JOIN with VALUES" }
func (largeArrayAny) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (largeArrayAny) RequiresSchema() bool               { return false }
func (largeArrayAny) RequiresExplain() bool              { return false }

func (largeArrayAny) Check(ctx *analyzer.Context) []analyzer.Finding {
    const threshold = 100
    var out []analyzer.Finding
    analyzer.Walk(ctx.AST, func(_ []string, n analyzer.ASTNode) bool {
        if analyzer.NodeKind(n) != "A_ArrayExpr" {
            return true
        }
        elems := analyzer.AsList(analyzer.Inner(n), "elements")
        if len(elems) < threshold {
            return true
        }
        loc := analyzer.AsInt(analyzer.Inner(n), "location")
        out = append(out, analyzer.Finding{
            Severity:   analyzer.SeverityWarn,
            Message:    fmt.Sprintf("ARRAY with %d elements; prefer a JOIN with VALUES or a temp table", len(elems)),
            Suggestion: "Replace `= ANY(ARRAY[…])` with `JOIN (VALUES (…),(…)) v(col) USING(col)`.",
            Evidence:   "GUIA_POSTGRES_ES_2.md §35.4",
            Location:   analyzer.Range{Start: loc, End: loc + 1},
        })
        return true
    })
    return out
}

func init() { Register(largeArrayAny{}) }
```

### Step 2 — Test fixture

```sh
cat > testdata/bad/large_array_any.sql <<'SQL'
-- expect: large_array_any
SELECT id FROM users WHERE id = ANY(ARRAY[1,2,3, …,150]);
SQL
```

### Step 3 — Add cases to the test matrix

In `internal/rules/rules_test.go`:

```go
"large_array_any": {
    {"big_array", "SELECT 1 FROM t WHERE id = ANY(ARRAY[" + repeatInt(150) + "])", true},
    {"small_array", "SELECT 1 FROM t WHERE id = ANY(ARRAY[1,2,3])", false},
},
```

### Step 4 — Run tests

```sh
go test ./...
```

If it all passes, the rule is already listed by `./pgopt --list-rules`.

---

## Lesson 12 — User-defined rules in JavaScript

When you want to encode project-specific conventions without forking the binary, rules can also be written in JavaScript and loaded at runtime. They are **opt-in**: without `--js-rules` they are not even discovered, and startup cost stays at zero.

### Quick look

```sh
./pgopt --js-rules --js-rules-dir ./rules-js query.sql
./pgopt --js-rules --list-rules   # JS rules now appear with [js]
```

The repository ships 11 example rules under `rules-js/`. Look at `rules-js/js_count_literal/` as a minimal template.

### Rule layout

```
rules-js/
    my_rule/
        manifest.json
        main.js
```

`manifest.json`:

```json
{
  "id": "my_rule",
  "description": "Single-line description, present tense.",
  "severity": "warn",
  "requiresSchema": false,
  "requiresExplain": false,
  "evidence": "src/backend/.../file.c"
}
```

`main.js` must export a `check(ctx)` function:

```js
function check(ctx) {
    const out = [];
    pg.forEachSelect(ctx.ast, function (sel) {
        // logic
    });
    return out;
}
```

The context your rule receives:

- `ctx.ast` — JSON tree from the real PostgreSQL parser (same shape Go rules see).
- `ctx.source` — original SQL text.
- `ctx.schema` — `null` unless the manifest sets `requiresSchema`.
- `ctx.explain` — `null` unless the manifest sets `requiresExplain`.

### The `pg.*` API

Heavy lifting is implemented in Go and exposed as globals on `pg`. Prefer these over walking the tree yourself in JS — they are ~10× to 50× faster.

| Function | Purpose |
|----------|---------|
| `pg.walk(tree, visit)` | DFS traversal. `visit(path, node)` returns `false` to prune. |
| `pg.forEachStmt(tree, fn)` | Iterates top-level statements: `fn(kind, inner, wrapper)`. |
| `pg.forEachSelect(tree, fn)` | Visits every `SelectStmt` (nested included). |
| `pg.findNodes(tree, kind)` | Returns every inner map with that wrapper. |
| `pg.firstLocation(node)` | First byte offset in the subtree. |
| `pg.nodeKind(n)` / `pg.inner(n)` | Single wrapper key / inner map. |
| `pg.asString/asInt/asList/asMap(n, key)` | Safe accessors. |
| `pg.columnRefs(tree)` / `pg.tableRefs(tree)` | Common column and table extraction. |
| `pg.finding(msg, sug?, locStart?, locEnd?)` | Finding builder for the common case. |

### Full example

```js
// rules-js/js_count_literal/main.js
function check(ctx) {
    const out = [];
    pg.findNodes(ctx.ast, "FuncCall").forEach(function (fc) {
        const nameList = pg.asList(fc, "funcname") || [];
        if (nameList.length === 0) return;
        const last = nameList[nameList.length - 1];
        if (pg.asString(pg.asMap(last, "String"), "sval").toLowerCase() !== "count") return;
        if (fc.agg_star) return;
        const args = pg.asList(fc, "args") || [];
        if (args.length !== 1 || pg.nodeKind(args[0]) !== "A_Const") return;
        const loc = pg.firstLocation(fc);
        out.push(pg.finding(
            "COUNT(<literal>) is identical to COUNT(*); prefer COUNT(*).",
            "Replace COUNT(1) with COUNT(*).",
            loc, loc + 1
        ));
    });
    return out;
}
```

### Testing

Drop a fixture under `testdata/bad-js/` with the expectation header and run `go test ./...`:

```sql
-- expect: my_rule
SELECT COUNT(1) FROM users;
```

The integration runner loads `rules-js/`, runs the combined Go+JS rule set, and verifies that your rule fires on the fixture.

### Error handling

If your `check()` throws, `pgopt` catches it and reports it as an `info` finding with the error message — the rest of the analysis keeps running. That prevents a new rule from silently breaking your build.

### See `docs/js-rules.md` for the full API reference.

---

## Lesson 13 — Panoramic view: the layers

Inventory at your fingertips:

```sh
./pgopt --list-rules               # Go only (50)
./pgopt --js-rules --list-rules    # includes JS rules ([js])
```

**Structure:**

```
 ┌───────────────────────────────────────────────────────────┐
 │  AST layer (31 rules)  — run WITHOUT a DB                 │
 │  Fast (ms), no side effects                               │
 │  Patterns: select *, large offset, cast, function on col, │
 │  leading-wildcard LIKE, NOT IN, ORDER BY random, subquery │
 │  in SELECT, recursive CTE without LIMIT, VACUUM FULL,     │
 │  TRUNCATE in tx, and more…                                │
 └───────────────────────────────────────────────────────────┘
                          ↓ (if --db)
 ┌───────────────────────────────────────────────────────────┐
 │  Schema-aware layer (10 rules) — reads pg_class, pg_index │
 │  SELECTs only; READ ONLY; safe                            │
 │  Patterns: missing_index, fk_without_index, duplicate_*,  │
 │  redundant_index, unused_index, missing_primary_key,      │
 │  stale_stats, partition_key_unused, timestamp_without_tz  │
 └───────────────────────────────────────────────────────────┘
                          ↓ (if --explain)
 ┌───────────────────────────────────────────────────────────┐
 │  EXPLAIN layer (9 rules) — runs real EXPLAIN ANALYZE      │
 │  Expensive (executes the query), READ ONLY                │
 │  Patterns: estimate_mismatch, external_sort, hash_batches,│
 │  ios_heap_fetches, seqscan_large, nestloop_seq_inner,     │
 │  parallel_underused, temp_buffers, cold_cache             │
 └───────────────────────────────────────────────────────────┘
                  ⊕ (if --js-rules, orthogonal)
 ┌───────────────────────────────────────────────────────────┐
 │  JavaScript layer (N project-defined rules)               │
 │  Opt-in; composes with any of the other layers            │
 │  Ideal for team conventions and domain rules              │
 └───────────────────────────────────────────────────────────┘
```

Rule of thumb I follow personally:
- **Commit / PR**: AST layer mandatory (fast, catches 90% of the problems).
- **Pre-deploy**: + schema-aware layer against staging.
- **Performance suspicion**: + EXPLAIN layer on the specific query.
- **Team conventions**: + `--js-rules` in repos where you have in-house rules.

---

## Lesson 14 — Diagnosing a real query, end to end

```sql
-- dashboard.sql
SELECT u.*, o.total_cents
  FROM users u
  JOIN orders o ON o.user_id = u.id
 WHERE lower(u.email) LIKE '%@example.com%'
   AND o.created_at::date = '2026-01-01'
 ORDER BY u.id
 LIMIT 50 OFFSET 5000;
```

### Step 1: offline analysis

```sh
./pgopt --verbose dashboard.sql
```

You will see:

- `select_star` on `u.*`.
- `function_on_column` on `lower(u.email)`.
- `like_leading_wildcard` on `'%@example.com%'`.
- `cast_in_where` on `o.created_at::date`.
- `offset_pagination` on `OFFSET 5000`.

**Five** anti-patterns in one query. Fixing each can yield 10–1000× improvement.

### Step 2: rewrite guided by the suggestions

```sql
SELECT u.id, u.email, u.name, o.total_cents
  FROM users u
  JOIN orders o ON o.user_id = u.id
 WHERE u.email LIKE '%@example.com'                          -- no leading %; btree can serve it
   AND o.created_at >= '2026-01-01'
   AND o.created_at <  '2026-01-02'                          -- range instead of ::date
 ORDER BY u.id
 LIMIT 50;                                                   -- no OFFSET
-- the app stores the last u.id seen and filters WHERE u.id > :last_id on the next page
```

### Step 3: verify

```sh
./pgopt --verbose dashboard_v2.sql
# ✓ no findings — query looks clean
```

### Step 4: with DB, check the plan

```sh
./pgopt --db "$DATABASE_URL" --explain dashboard_v2.sql
```

If you see `explain_estimate_mismatch`, fix stats. If you see `explain_seqscan_large`, create the missing index. All clean → deploy.

---

## Summary: the daily flow

```
 [you write SQL]
      │
      ▼
 pgopt offline  ──────► AST findings (fast, local)
      │
      │ none
      ▼
 pgopt --db     ──────► schema-aware findings
      │
      │ none
      ▼
 pgopt --explain ─────► real-plan findings (expensive, definitive)
      │
      │ none
      ▼
 deploy with confidence
```

Each step is cheap; you escalate only when the previous one found nothing. Heuristic: **90% of problems are caught offline. 9% with `--db`. 1% need `--explain`.**

JS rules are orthogonal: you can turn them on at any step of the flow with `--js-rules`.

---

## Lesson 15 — Tricks and FAQs

### Can I run it on a file with multiple statements?

Yes. `pgopt` parses the whole content as a list of `stmts`. Every rule sees every statement. Handy for migration files.

```sh
./pgopt migration_042.sql
```

### Can I permanently disable a rule?

Per project, via a shell wrapper or a `.pgoptrc` (on the roadmap). Today the way is:

```sh
alias pgopt='pgopt --rules "all,-is_null_in_where,-union_vs_union_all"'
```

### How does it fit with `golangci-lint` or `sqlfluff`?

It does not overlap: `sqlfluff` is style/format; `golangci-lint` is Go. `pgopt` is about **PostgreSQL semantics**. Typical order: sqlfluff first (format/style), pgopt next (engine-specific).

### The parser rejects my valid SQL

`pg_query_go` embeds the real PostgreSQL 17 parser. If your SQL uses Oracle/MSSQL/MySQL syntax, it will not parse. Examples: `TOP N`, `NOLOCK`, `ROWNUM`. That is correct: it is not valid PostgreSQL.

### `--explain` takes too long

`EXPLAIN ANALYZE` executes the query. If the query takes 30 seconds, analysis takes 30 seconds. Options:
- Narrow the query (add a `LIMIT` before analyzing).
- Run against staging with representative but smaller data.
- Use `EXPLAIN` without `ANALYZE` (but you lose actual-vs-estimated comparisons — **not worth it**).

### My JS rules don't show in `--list-rules`

You need to pass `--js-rules`. Without that flag `pgopt` does not even open the directory — intentional, to guarantee zero cost when unused.

### A JS rule is blowing up the analysis

Runtime errors in JS are converted into `info` findings with the exception message. The rest of the rules keep running. Look in the output for a finding carrying your rule id to see the message.

---

## To keep learning

- `./pgopt --list-rules` — every rule with its description.
- `./pgopt --verbose` — always use this to read explanations and code references.
- `docs/js-rules.md` — full API reference for JavaScript rules.
- `docker/init.sql` and `docker/seed.sql` — the demo schema, commented with each anti-pattern.
- Rules cite files from the PostgreSQL source tree (`src/backend/...`), browsable at <https://github.com/postgres/postgres>. When you see `GUIA_POSTGRES_ES_2.md §N.N` the reference is to a companion engine guide (external material) that will be linked alongside the stable release.

If a rule bothers you or a check is missing, open an issue or send a PR — this project is designed to grow with real cases.

— End of tutorial —
