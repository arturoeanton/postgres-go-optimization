// js_union_branch_order_by — ORDER BY inside a UNION branch.
//
// transformSetOperationStmt in parser/analyze.c rejects a bare ORDER BY
// on individual branches and requires parentheses, but even when the
// author wraps a branch in parens the planner will ignore the inner
// ordering unless a LIMIT is present — set operations do not preserve
// row order. The ORDER BY is therefore misleading in the branch.
function check(ctx) {
    const out = [];
    pg.forEachSelect(ctx.ast, function (sel) {
        const op = pg.asString(sel, "op");
        if (!op || op === "SETOP_NONE") return;
        ["larg", "rarg"].forEach(function (side) {
            const arm = pg.asMap(sel, side);
            if (!arm) return;
            const sort = pg.asList(arm, "sortClause");
            if (!sort || sort.length === 0) return;
            const limit = pg.asMap(arm, "limitCount");
            if (limit) return; // ORDER BY + LIMIT is meaningful
            const first = sort[0];
            const loc = pg.firstLocation(first);
            out.push(pg.finding(
                "ORDER BY in a UNION/INTERSECT branch without LIMIT is ignored by the set-operation rewrite",
                "Move the ORDER BY to the outer query, or pair it with a LIMIT if you want a top-N-per-branch.",
                loc, loc + 1
            ));
        });
    });
    return out;
}
