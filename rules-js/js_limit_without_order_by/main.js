// js_limit_without_order_by — LIMIT without ORDER BY.
//
// The Limit node in executor/nodeLimit.c simply passes through the first
// N tuples it receives. Without an ORDER BY, the planner is free to pick
// any physical order (heap scan, index scan, hash) and the slice observed
// by the client becomes an implementation detail that can drift after
// VACUUM, analyze, or plan changes. This is a frequent source of flaky
// pagination bugs.
function check(ctx) {
    const out = [];
    pg.forEachSelect(ctx.ast, function (sel) {
        const limit = pg.asMap(sel, "limitCount");
        if (!limit) return;
        const sort = pg.asList(sel, "sortClause");
        if (sort && sort.length > 0) return;
        // Ignore LIMIT on set operations where the outer query may add ORDER BY.
        if (pg.asString(sel, "op") && pg.asString(sel, "op") !== "SETOP_NONE") return;
        const loc = pg.firstLocation(limit);
        out.push(pg.finding(
            "LIMIT without ORDER BY returns an arbitrary slice of the result set",
            "Add an ORDER BY clause with a deterministic tiebreaker (e.g. primary key) before the LIMIT.",
            loc, loc + 1
        ));
    });
    return out;
}
