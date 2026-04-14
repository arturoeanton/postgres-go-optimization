// js_double_negation — detects NOT (NOT x).
//
// canonicalize_qual in optimizer/util/clauses.c collapses NOT NOT during
// plan prep, so the construct is free at runtime. It is usually an
// accidental artifact of two merges touching the same predicate, and
// reviewing it often reveals that the author intended a different
// condition to remain negated.
function check(ctx) {
    const out = [];
    pg.findNodes(ctx.ast, "BoolExpr").forEach(function (be) {
        if (pg.asString(be, "boolop") !== "NOT_EXPR") return;
        const args = pg.asList(be, "args") || [];
        if (args.length !== 1) return;
        const inner = args[0];
        if (pg.nodeKind(inner) !== "BoolExpr") return;
        const innerBe = pg.inner(inner);
        if (pg.asString(innerBe, "boolop") !== "NOT_EXPR") return;
        const loc = pg.asInt(be, "location");
        out.push(pg.finding(
            "NOT (NOT x) is logically x; review whether the double negation is intentional",
            "Remove both NOTs, or negate the other half of the expression if that was the intent.",
            loc, loc + 1
        ));
    });
    return out;
}
