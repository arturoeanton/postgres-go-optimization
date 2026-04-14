// js_redundant_coalesce — COALESCE with duplicate column references.
//
// COALESCE(a, a) always evaluates to `a` because the first non-null arg
// is returned. The executor in execExprInterp.c still visits every
// argument at setup time. More importantly the second appearance is
// usually copy-paste noise that masked a real fallback the author meant
// to write (e.g. COALESCE(a, b)).
function check(ctx) {
    const out = [];
    pg.findNodes(ctx.ast, "CoalesceExpr").forEach(function (ce) {
        const args = pg.asList(ce, "args") || [];
        if (args.length < 2) return;
        const seen = {};
        for (let i = 0; i < args.length; i++) {
            const key = columnKey(args[i]);
            if (!key) continue;
            if (seen[key]) {
                const loc = pg.asInt(ce, "location");
                out.push(pg.finding(
                    "COALESCE repeats the same column reference; later copies are unreachable",
                    "Drop the duplicate, or replace it with the fallback expression you actually wanted.",
                    loc, loc + 1
                ));
                return;
            }
            seen[key] = true;
        }
    });
    return out;
}

function columnKey(node) {
    if (pg.nodeKind(node) !== "ColumnRef") return "";
    const fields = pg.asList(pg.inner(node), "fields") || [];
    const parts = [];
    for (let i = 0; i < fields.length; i++) {
        const f = fields[i];
        const s = pg.asString(pg.asMap(f, "String"), "sval");
        if (!s) return ""; // A_Star or other — treat as unknown
        parts.push(s);
    }
    return parts.join(".");
}
