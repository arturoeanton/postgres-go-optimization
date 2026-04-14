# User-defined rules in JavaScript

`pgopt` ships with 50+ rules written in Go. For teams that want to encode
project-specific conventions without forking the binary, rules can also
be authored in JavaScript and loaded at startup.

JS rules are **opt-in**: they are only discovered and executed when the
CLI is invoked with `--js-rules`. With the flag off, behaviour is
identical to the stock build and there is zero runtime cost.

## Quick start

```sh
pgopt --js-rules --js-rules-dir ./my-rules query.sql
```

The default `--js-rules-dir` is `rules-js/` relative to the working
directory. The repository ships ten example rules there that you can
use as templates.

## Directory layout

Every rule lives in its own folder:

```
rules-js/
    my_rule/
        manifest.json
        main.js
```

Directories that lack a `manifest.json` are skipped, so the tree can
hold `README.md`, drafts, or scratch space without breaking the loader.

## Manifest schema

```json
{
  "id": "my_rule",
  "description": "Short, single-line description shown by --list-rules",
  "severity": "warn",
  "requiresSchema": false,
  "requiresExplain": false,
  "evidence": "src/backend/optimizer/.../file.c",
  "entry": "main.js"
}
```

| Field             | Required | Notes                                             |
|-------------------|----------|---------------------------------------------------|
| `id`              | yes      | Unique across Go and JS rules. Used for selection. |
| `description`     | yes      | One sentence, present tense.                       |
| `severity`        | yes      | `info` \| `warn` \| `error`.                       |
| `requiresSchema`  | no       | If true, the rule is skipped unless `--db` is set. |
| `requiresExplain` | no       | If true, requires `--db --explain`.                |
| `evidence`        | no       | Pointer to PG source or guide section.             |
| `entry`           | no       | JS filename. Defaults to `main.js`.                |

## Rule authoring contract

`main.js` must define a top-level `check(ctx)` function. It receives a
context object and returns an array of findings.

```js
function check(ctx) {
    // ctx.ast     — pg_query JSON tree (same shape Go rules see)
    // ctx.source  — original SQL text
    // ctx.schema  — null unless the rule manifest sets requiresSchema
    // ctx.explain — null unless the rule manifest sets requiresExplain
    return [
        {
            message:     "...",
            explanation: "...",     // optional; shown with --verbose
            suggestion:  "...",     // optional
            severity:    "warn",    // optional; defaults to manifest
            evidence:    "...",     // optional; overrides manifest
            location:    { start: 0, end: 1 }  // optional; byte offsets
        }
    ];
}
```

Returning `[]`, `null`, `undefined`, or a single object (instead of an
array) is also accepted for convenience.

## The `pg` helper surface

Heavy AST work is exposed as Go-backed functions on the global `pg`
object. Prefer these over JS-side traversal — the helpers are ~10× to
50× faster than doing the same walking in interpreted JS.

### Traversal

| Function                            | Returns              | Purpose                                              |
|-------------------------------------|----------------------|------------------------------------------------------|
| `pg.walk(tree, visit)`              | `undefined`          | Depth-first visit. Return `false` from visit to prune. |
| `pg.forEachStmt(tree, fn)`          | `undefined`          | Iterates top-level statements; `fn(kind, inner, wrapper)`. |
| `pg.forEachSelect(tree, fn)`        | `undefined`          | Visits every `SelectStmt` (top-level or nested). |
| `pg.findNodes(tree, kind)`          | `array<inner>`       | All inner maps whose wrapper kind is `kind`.        |
| `pg.firstLocation(node)`            | `number`             | First `location` byte offset in a subtree.          |

### Accessors

| Function                     | Returns        |
|------------------------------|----------------|
| `pg.nodeKind(node)`          | `string` (wrapper key, e.g. `"SelectStmt"`) |
| `pg.inner(node)`             | Inner map of a single-keyed wrapper.       |
| `pg.asString(map, key)`      | `string` or `""`                             |
| `pg.asInt(map, key)`         | `number` (0 if missing)                     |
| `pg.asList(map, key)`        | `array` or `null`                            |
| `pg.asMap(map, key)`         | Inner map or `null`                          |

### Convenience

| Function                                                    | Returns         |
|-------------------------------------------------------------|-----------------|
| `pg.columnRefs(tree)`                                       | `array<string>` (dotted names) |
| `pg.tableRefs(tree)`                                        | `array<string>` (schema-qualified when available) |
| `pg.finding(message, suggestion?, locStart?, locEnd?)`      | Finding object ready to push  |

## Worked example

A rule that flags `COUNT(<literal>)`:

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
            "COUNT(<literal>) is identical to COUNT(*); prefer COUNT(*) for clarity",
            "Replace COUNT(1) with COUNT(*).",
            loc, loc + 1
        ));
    });
    return out;
}
```

## Testing your rules

Drop a fixture under `testdata/bad-js/` with a first-line expectation
header and run `go test ./...`:

```sql
-- expect: js_count_literal
SELECT COUNT(1) FROM users;
```

The integration suite loads `rules-js/`, runs the combined Go + JS
ruleset, and asserts that the expected rule fired.

## Performance notes

- The goja runtime is shared across rules and mutex-protected; pgopt
  processes one query at a time so contention is a non-issue.
- The AST is passed to JS as a wrapped Go map. Accessing fields through
  `pg.*` helpers is faster than walking via native JS because the
  traversal itself runs in Go.
- Runtime errors in a user rule do not abort the run — they surface as
  an `info`-level finding so the author can see and fix them.

## Security model

JavaScript rules run inside a [goja](https://github.com/dop251/goja)
runtime embedded in `pgopt`. The runtime is **deliberately stripped** of
everything outside pure ECMAScript. Concretely, a rule author can:

- Read from the `ctx` object that `pgopt` passes in.
- Call the helpers exposed as `pg.*`.
- Use the built-in ECMAScript surface: `Array`, `String`, `Object`,
  `Math`, `JSON`, `Map`, `Set`, `Promise`, regular expressions, and
  the usual primitive operators.

A rule author **cannot**, from inside a rule:

- Read or write files (no `require('fs')`, no `readFile`, no `import`).
- Open sockets or make HTTP requests (no `fetch`, no `XMLHttpRequest`,
  no DNS).
- Spawn processes, read environment variables, or touch the clipboard.
- Schedule work beyond the current call: `setTimeout`, `setInterval`,
  `queueMicrotask`, and the DOM event loop do not exist.
- Access the Go host through reflection: goja does not expose Go
  interop beyond the explicit bindings pgopt installs.

This means that a rule someone copies from an untrusted gist can
produce bogus findings — which is bad but bounded — but cannot exfiltrate
your schema, read a `.env` file, or call out to a remote server.

Two operational consequences:

1. If your rule *legitimately* needs to reach the catalog or the query
   plan, it must ask for it via the manifest (`requiresSchema: true`
   and/or `requiresExplain: true`). The host populates `ctx.schema` and
   `ctx.explain`; there is no other path.
2. A buggy rule that throws an exception or loops forever does not
   bring the CLI down. Exceptions become `info`-level findings; the
   user sees the message and can open an issue with the rule author.
   (A CPU-bound infinite loop is a real hazard today — that is tracked
   for a later release, where we will add a per-rule timeout.)

## Limitations

- No `require` / `import`. Each `main.js` is loaded in isolation. If you
  need shared helpers, inline them at the top of the file or vendor a
  small utility set via a build step.
- No standard DOM / Node APIs. The VM is pure ECMAScript (ES5.1 with
  several ES6 additions via goja).
- `ctx.schema` and `ctx.explain` are currently read-only projections of
  the Go structs; their object graph is auto-wrapped and stable but
  undocumented beyond that. Consult `internal/schema` and
  `internal/explain` to see the field names.
