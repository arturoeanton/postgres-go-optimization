---
name: Rule proposal
about: Propose a new pgopt rule backed by PostgreSQL engine behaviour
title: "[rule] "
labels: rule-proposal
---

## Rule ID

<!-- snake_case; unique across Go and JS rules. Example: large_order_by_offset -->

## What it detects

<!-- One sentence describing the SQL pattern. Include a minimal offending snippet. -->

```sql
-- offending example
```

## Why it matters (engine evidence)

<!--
Point at the PostgreSQL source file(s) or `GUIA_POSTGRES_ES_2.md`
section that proves the pattern is bad. Findings without evidence are
not accepted — this is the project's defining trait.
-->

- `src/backend/...`
- `GUIA_POSTGRES_ES_2.md §...`

## Suggested rewrite

```sql
-- what the user should write instead
```

## Severity

- [ ] info — stylistic, low-risk
- [ ] warn — likely performance impact or subtle bug
- [ ] error — demonstrable regression or footgun

## Implementation hint (optional)

- [ ] AST-only (no DB needed)
- [ ] Schema-aware (requires `--db`)
- [ ] EXPLAIN-based (requires `--db --explain`)
- [ ] Could be authored as a JavaScript rule (see `docs/js-rules.md`)
