# Security Policy

## Supported versions

`pgopt` is in pre-1.0 development. Security fixes are only backported to
the most recent tagged release and to `main`.

| Version         | Status        |
|-----------------|---------------|
| `main`          | Supported     |
| `0.1.x` latest  | Supported     |
| older pre-releases | Best-effort |

## Reporting a vulnerability

**Please do not open public GitHub issues for security reports.**

If you believe you have found a vulnerability — for example, a path
through which a specially crafted SQL snippet can escape the
`READ ONLY` guarantees, or a way for a malicious JavaScript rule to
reach network or filesystem resources — send the details privately to
the project maintainer:

- Email: the `maintainer` line in `CONTRIBUTING.md`, or the email
  address recorded in `go.mod`.
- Subject prefix: `[security]`.

Include, where applicable:

- A reproducer (minimal SQL snippet, or a minimal JS rule and CLI
  invocation).
- The version of `pgopt` (`pgopt --version`) and the Go toolchain
  (`go version`) you tested against.
- Your assessment of the impact.

You will receive an acknowledgement within 72 hours. We aim to issue a
fix and a coordinated disclosure within 30 days of the acknowledgement,
depending on severity.

## Scope

The following are in scope:

- The CLI binary (`cmd/pgopt`) and every first-party package under
  `internal/`.
- The example JavaScript rules shipped under `rules-js/`.
- The documentation and CI workflows, to the extent they could mislead
  a user into an unsafe configuration.

The following are **out of scope**:

- Vulnerabilities in upstream dependencies (`pgx`, `pg_query_go`,
  `goja`, the Go standard library). Report those to their respective
  maintainers; we will update once a fix is available.
- Denial-of-service via intentionally crafted inputs that require an
  attacker to already have permission to run `pgopt` on arbitrary SQL.
  Those are accepted risks of the tool.

## Safety guarantees the project commits to

`pgopt` goes to some length to avoid damaging the database it connects
to. These are the invariants we treat as non-negotiable; a bug that
breaks any of them is a security issue:

- Every database connection is put into
  `SET default_transaction_read_only = on` on open.
- The `EXPLAIN` runner wraps the user query in `BEGIN READ ONLY` and
  always rolls back, so `EXPLAIN (ANALYZE)` on a DML statement does
  not execute the mutation.
- `pgopt` never writes to the database. The schema loader and the
  EXPLAIN runner are the only code paths that issue SQL, and both are
  restricted to `SELECT` and `EXPLAIN`.
- User-supplied JavaScript rules run inside a `goja` runtime that does
  not expose network, filesystem, process, or timer APIs by default.
  See `docs/js-rules.md` for the exact surface.

## Credit

We list security reporters in the release notes unless they prefer to
remain anonymous.
