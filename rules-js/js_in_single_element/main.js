// js_in_single_element — col IN (x) with one element.
//
// transformAExprIn in parser/parse_expr.c rewrites `col IN (x,y,...)` into
// a ScalarArrayOpExpr. For a one-element list the planner will usually
// simplify to equality, but humans reading the SQL often assume multiple
// values are expected and carry the IN through refactors, widening bugs.
// Prefer the explicit `=` form when the list is a singleton.
function check(ctx) {
    const out = [];
    pg.findNodes(ctx.ast, "A_Expr").forEach(function (e) {
        if (pg.asString(e, "kind") !== "AEXPR_IN") return;
        const name = pg.asList(e, "name") || [];
        if (name.length === 0) return;
        const opName = pg.asString(pg.asMap(name[0], "String"), "sval");
        if (opName !== "=" && opName !== "<>") return;
        // rexpr is a List wrapper in pg_query v6: {List: {items: [...]}}
        const rWrap = pg.asMap(e, "rexpr");
        const list = pg.asMap(rWrap, "List");
        const items = pg.asList(list, "items");
        if (!items || items.length !== 1) return;
        const loc = pg.asInt(e, "location");
        out.push(pg.finding(
            "IN / NOT IN with a single element is equivalent to = / <> and clearer that way",
            "Replace `col IN (x)` with `col = x` (or NOT IN → <>).",
            loc, loc + 1
        ));
    });
    return out;
}
