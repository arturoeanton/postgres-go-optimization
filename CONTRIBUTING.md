# Contributing to pgopt

Thanks for considering a contribution. `pgopt` aims to be a small,
focused, reproducible linter for PostgreSQL queries, grounded in the
engine source. Every rule and every claim in the docs should be
verifiable against a file and line in the PostgreSQL tree.

Please read this document before sending a pull request — it is short.

## Requirements

- Go **1.22** or newer.
- Docker (only for the integration demo with a real PostgreSQL instance).
- Git.

## Getting the code

```sh
git clone https://github.com/arturoeanton/postgres-go-optimization.git
cd postgres-go-optimization
make build        # produces ./pgopt
```

## Running the test suite

```sh
make test                     # equivalent to: go test ./...
go test -race -count=1 ./...  # what CI runs
go test -cover ./...          # with coverage
```

All pull requests are gated on `go test -race ./...` passing on Linux
and macOS with Go 1.22 and 1.23.

## Trying the Docker demo

```sh
make docker-up
export PGOPT_DB="postgres://pgopt:pgopt@localhost:55432/pgopt?sslmode=disable"
./pgopt --db "$PGOPT_DB" --explain testdata/demo/bad_query_1.sql
make docker-down
```

## Project layout

```
cmd/pgopt/           CLI entry point + cli_test.go
internal/
  analyzer/          AST walker, Finding type, Context, Run() orchestrator
  rules/             one file per Go rule; rules self-register via init()
  jsrules/           JavaScript rule loader (goja) + Go-backed helpers
  schema/            read-only catalog loader (pg_class, pg_index, ...)
  explain/           EXPLAIN runner (READ ONLY tx, FORMAT JSON)
  rewriter/          text-patch engine (scaffolded; no rule emits patches yet)
  report/            text (colorized) + JSON renderers
rules-js/            example JavaScript rules (opt-in via --js-rules)
docker/              init.sql + seed.sql for the demo database
testdata/
  bad/               one .sql per Go rule, header: "-- expect: <rule_id>"
  bad-js/            one .sql per JS rule, same header convention
  good/              queries that should produce zero findings
  demo/              queries that exercise schema-aware + EXPLAIN
```

## Writing a new Go rule

1. Create `internal/rules/my_rule.go` implementing `rules.Rule`:
   ```go
   type myRule struct{}
   func (myRule) ID() string                         { return "my_rule" }
   func (myRule) Description() string                { return "…" }
   func (myRule) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
   func (myRule) RequiresSchema() bool               { return false }
   func (myRule) RequiresExplain() bool              { return false }
   func (myRule) Check(ctx *analyzer.Context) []analyzer.Finding { … }
   func init() { Register(myRule{}) }
   ```
2. Add a fixture at `testdata/bad/my_rule.sql` starting with
   `-- expect: my_rule`.
3. Add positive and negative cases to the matrix at the top of
   `internal/rules/rules_test.go`.
4. Add a row to the rule table in `README.md`.
5. If your rule asserts a non-trivial planner behaviour, back it with an
   `Evidence` string that points at a PostgreSQL source file and
   function. Rules without evidence are not accepted.

## Writing a new JavaScript rule

See [`docs/js-rules.md`](docs/js-rules.md) for the full API. The short
version:

1. Create `rules-js/my_rule/manifest.json` and `rules-js/my_rule/main.js`.
2. `main.js` must expose `function check(ctx)` that returns findings.
3. Add a fixture at `testdata/bad-js/my_rule.sql` with the usual header.
4. Run `go test ./...` — the integration suite picks the rule up
   automatically.

## Style

- Rules should read top-to-bottom in under ~100 lines. If longer, split.
- No `fmt.Println` or `log.Printf` in library code; use `report.*`.
- Rules must be pure: they receive `ctx` and return findings. Do not
  mutate `ctx`.
- Schema loader and EXPLAIN runner force `SET default_transaction_read_only = on`
  and wrap EXPLAIN in `BEGIN READ ONLY`. Never regress this.
- Never add a new dependency without a real reason. The dep list is
  intentionally small (`pgx/v5`, `pg_query_go/v6`, `goja`, stdlib).

## Commit messages

Prefix with one of the following, followed by a short imperative
summary:

- `rule:` — adding or changing a rule.
- `core:` — analyzer, walker, CLI, runtime infrastructure.
- `docs:` — documentation only.
- `docker:` — the seeded database used for the demo.
- `ci:` — GitHub Actions or build tooling.

Examples:

```
rule: add js_limit_without_order_by
core: gracefully surface JS rule runtime errors as findings
docs: document .pgoptignore resolution order
```

Keep the subject under 72 characters. Bodies are optional; if present,
explain **why** rather than **what**.

## Pull request checklist

Before opening a PR, please confirm:

- [ ] `go test -race -count=1 ./...` passes locally.
- [ ] `go vet ./...` is clean.
- [ ] If you added or changed a rule, you updated `README.md`.
- [ ] If you added a rule, you added positive and negative test cases.
- [ ] If your change is user-facing, you added a `CHANGELOG.md` entry.

## Reporting a bug

Open an issue with:

1. A minimal SQL snippet that reproduces the behaviour.
2. Your `pgopt` version (`pgopt --version` once available, otherwise
   the commit hash).
3. What you expected vs what you saw.
4. If relevant, the output of `pgopt --format json --verbose`.

## Reporting a security issue

If you believe you have found a vulnerability (for example, an injection
path through the SQL source that escapes the read-only guarantees),
please do not open a public issue. Email the maintainer listed in
`go.mod` directly.

## Licensing of contributions

By contributing, you agree that your changes are released under the
GNU General Public License, version 3 or later, the same licence that
covers the project. See `LICENSE` for the full text.
