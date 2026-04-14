// js_order_by_in_subquery — ORDER BY in a derived table without LIMIT.
//
// pull_up_subqueries in optimizer/prep/prepjointree.c flattens subqueries
// into the outer query and drops their ORDER BY because a set-returning
// subquery has no guaranteed order anyway. The ORDER BY wastes planning
// time and misleads readers; it only survives when combined with LIMIT
// (then the subquery becomes a top-N).
function check(ctx) {
    const out = [];
    // A derived table appears as RangeSubselect; its `subquery` is the SelectStmt.
    pg.findNodes(ctx.ast, "RangeSubselect").forEach(function (rs) {
        const sub = pg.asMap(rs, "subquery");
        const inner = pg.inner(sub);
        if (!inner) return;
        const sort = pg.asList(inner, "sortClause");
        if (!sort || sort.length === 0) return;
        const limit = pg.asMap(inner, "limitCount");
        if (limit) return;
        const loc = pg.firstLocation(sort[0]);
        out.push(pg.finding(
            "ORDER BY in a derived table without LIMIT is discarded when the subquery is pulled up",
            "Move the ORDER BY to the outer query, or pair it with a LIMIT if this is a top-N subquery.",
            loc, loc + 1
        ));
    });
    return out;
}
