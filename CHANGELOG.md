# Changelog

All notable changes to `pgopt` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `pgopt --version` prints the version embedded at build time via
  `-ldflags -X main.Version=...`. The Makefile wires this up from
  `git describe`.
- Inline pragmas in SQL: `-- pgopt:ignore=rule_id[,rule_id]` silences
  the listed rules for the rest of the file; `-- pgopt:ignore-next`
  silences any finding that lands on the next non-blank, non-comment
  line.
- `.github/ISSUE_TEMPLATE/*` and `pull_request_template.md` to guide
  new contributors.
- `.github/dependabot.yml` for weekly Go module and GitHub Actions
  updates.
- `SECURITY.md` describing the coordinated-disclosure process and the
  safety invariants the project commits to.
- `docs/js-rules.md` now contains a "Security model" section covering
  what the `goja` sandbox exposes to JavaScript rules.
- Release plumbing: `.goreleaser.yaml` and `.github/workflows/release.yml`
  build signed archives for Linux and macOS (amd64+arm64) on every
  `v*` tag.
- CI integration job: a live PostgreSQL container is seeded from
  `docker/` and exercised with schema-aware and EXPLAIN-based rules on
  every push.

### Changed

- `SIGINT` / `SIGTERM` during schema load or EXPLAIN now cancels the
  in-flight PostgreSQL query via the driver's context, and `pgopt`
  exits cleanly with a short "cancelled" message instead of a Go
  traceback.
- Database errors printed to stderr are passed through a redactor that
  masks passwords embedded in DSNs (`postgres://user:pass@…` and
  `password=secret` forms alike).
- `implicit_cross_join` is downgraded from `warn` to `info`. The
  pattern is frequently intentional (calendar × regions style product
  tables) and a false positive on `warn` was the most common "why does
  my CI fail" report during the alpha.
- `is_null_in_where` no longer fires when `IS NULL` is embedded in a
  multi-predicate `WHERE` chain. The partial-index suggestion the rule
  makes is only useful when the IS NULL is the driving predicate.

## [0.1.0-alpha] — 2026-04-14

First public pre-release. Feature-complete for AST / schema-aware /
EXPLAIN analysis; API surface may still change before the stable 0.1.0.

### Added

- AST layer with 31 rules covering common PostgreSQL anti-patterns
  (`SELECT *`, large `OFFSET`, function-on-column, leading-wildcard
  `LIKE`, `NOT IN` with a nullable subquery, `ORDER BY random()`,
  correlated subquery in `SELECT`, recursive CTE without guard, and
  more).
- Schema-aware layer with 10 rules that read the live catalog
  (`missing_index`, `fk_without_index`, `duplicate_index`,
  `redundant_index`, `unused_index`, `missing_primary_key`,
  `stale_stats`, `partition_key_unused`, `timestamp_without_tz`).
- EXPLAIN layer with 9 rules that analyse the actual plan produced by
  `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` inside a `READ ONLY`
  transaction.
- User-defined rules in JavaScript (opt-in via `--js-rules`). Rules
  live in `rules-js/<id>/{manifest.json,main.js}` and receive a
  Go-backed helper surface under the global `pg` object. Eleven example
  rules shipped.
- `.pgoptignore` file at the working directory is read automatically;
  rule IDs listed there are skipped. Override with `--ignore-file` or
  disable with `--no-ignore-file`.
- CLI flags: `--db`, `--rules`, `--format`, `--min-severity`,
  `--fail-on`, `--color`, `--quiet`, `--verbose`, `--explain`,
  `--list-rules`, `--js-rules`, `--js-rules-dir`, `--ignore-file`,
  `--no-ignore-file`.
- JSON and text (colorised) output formats.
- Docker Compose stack that spins up PostgreSQL 17 with a seeded schema
  containing intentional anti-patterns for integration testing.
- Tutorials in English and Spanish under `docs/`.
- JavaScript rule API reference at `docs/js-rules.md`.

### Safety

- Schema loader and EXPLAIN runner force
  `SET default_transaction_read_only = on`.
- EXPLAIN runs inside `BEGIN READ ONLY` and always rolls back, so
  `EXPLAIN (ANALYZE)` on `INSERT` / `UPDATE` / `DELETE` statements does
  not execute the mutation.

### Known limitations

- Some AST rules (`implicit_cross_join`, `boolean_equals_true`,
  `is_null_in_where`) can produce false positives on idioms that are
  legitimate in context. They will be tuned before the stable 0.1.0
  release. Silence them per-project via `.pgoptignore`.
- No in-query pragma (`-- pgopt:ignore=...`) yet; the `.pgoptignore`
  file is the supported way to silence a rule for a project.
- The `schema` and `explain` packages are covered only by tests that
  require a live PostgreSQL connection (exercised by the Docker demo
  and the fixtures job in CI). Unit coverage on those two packages is
  low by design.
- PostgreSQL version compatibility has been exercised on 17 only.
  16 and 18 are on the 0.2.0 roadmap.

### Fixed during pre-release hardening

- `boolean_equals_true` now reads the literal location from the inner
  `A_Const` node. The previous implementation read from the wrapper and
  never fired, so `WHERE flag = true` was silently ignored.
- `window_empty`, `cte_unused`, and `recursive_cte_no_limit` now detect
  the unwrapped AST shapes that `pg_query_go` v6 emits. They previously
  required a wrapper key that the parser does not produce for inline
  `OVER (…)` clauses or `withClause` attached directly to `SelectStmt`.
- `recursive_cte_no_limit` recognises a `LIMIT` on the enclosing outer
  query as a valid termination bound, not only `LIMIT` inside the CTE
  body.

### Test coverage (as of this release)

| Package | Coverage |
|---------|---------:|
| `internal/report` | 97 % |
| `internal/analyzer` | 95 % |
| `internal/rewriter` | 95 % |
| `internal/rules` | 90 % |
| `internal/jsrules` | 86 % |
| `cmd/pgopt` | 82 % |
| `internal/schema` | 36 % (live-DB bound) |
| `internal/explain` | — (live-DB bound) |

[Unreleased]: https://github.com/arturoeanton/postgres-go-optimization/compare/v0.1.0-alpha...HEAD
[0.1.0-alpha]: https://github.com/arturoeanton/postgres-go-optimization/releases/tag/v0.1.0-alpha
