// js_nested_case_when — detects CASE nested inside CASE beyond two levels.
//
// Deep CASE nests are both a readability trap and a hint that the logic
// could likely be expressed as a lookup table, a COALESCE chain, or a
// GREATEST/LEAST expression. parse_expr.c:transformCaseExpr walks each
// branch independently, so deeply nested forms multiply plan-time work
// without necessarily improving the evaluation speed.
function check(ctx) {
    const out = [];
    const MAX_DEPTH = 2;

    function depth(node) {
        if (!node) return 0;
        if (pg.nodeKind(node) !== "CaseExpr") return 0;
        const inner = pg.inner(node);
        const branches = pg.asList(inner, "args") || [];
        let childMax = 0;
        branches.forEach(function (b) {
            const cw = pg.inner(b);
            if (!cw) return;
            const result = pg.asMap(cw, "result");
            const sub = depthOfSubtree(result);
            if (sub > childMax) childMax = sub;
        });
        const def = pg.asMap(inner, "defresult");
        const dsub = depthOfSubtree(def);
        if (dsub > childMax) childMax = dsub;
        return 1 + childMax;
    }

    function depthOfSubtree(n) {
        if (!n) return 0;
        let max = 0;
        pg.walk(n, function (_, x) {
            if (pg.nodeKind(x) === "CaseExpr") {
                const d = depth(x);
                if (d > max) max = d;
                return false; // depth() will recurse
            }
            return true;
        });
        return max;
    }

    pg.findNodes(ctx.ast, "CaseExpr").forEach(function (ce) {
        // Only flag the outermost CASE so we do not emit N findings for
        // the same nesting; outermost has depth equal to total levels.
        const wrapper = { CaseExpr: ce };
        const d = depth(wrapper);
        if (d <= MAX_DEPTH) return;
        const loc = pg.asInt(ce, "location");
        out.push(pg.finding(
            "CASE nested more than " + MAX_DEPTH + " levels deep; consider a lookup, COALESCE chain, or GREATEST/LEAST",
            "Flatten the CASE — each extra level costs a branch evaluation per input row.",
            loc, loc + 1
        ));
    });

    // Deduplicate by location so inner CASE nodes that also exceed the
    // threshold do not double-report the same nest.
    const seen = {};
    return out.filter(function (f) {
        const k = f.location.start + ":" + f.location.end;
        if (seen[k]) return false;
        seen[k] = true;
        return true;
    });
}
