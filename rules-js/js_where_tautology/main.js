// js_where_tautology — constant-true predicates at the WHERE root.
//
// `WHERE 1=1`, `WHERE TRUE`, `WHERE 'x' = 'x'` are optimizer no-ops. The
// planner folds them out cheaply, but their presence usually indicates a
// query-building library that forgot to omit the prefix — which often
// correlates with missing downstream conditions. Flag as info so humans
// can verify the AND chain is complete.
function check(ctx) {
    const out = [];
    pg.forEachSelect(ctx.ast, function (sel) {
        const w = pg.asMap(sel, "whereClause");
        if (!w) return;
        const tauto = isTautology(w);
        if (!tauto) return;
        const loc = pg.firstLocation(w);
        out.push(pg.finding(
            "WHERE clause is a constant tautology; likely an ORM-generated placeholder",
            "Remove the `1=1` / `TRUE` and ensure the real predicates were appended.",
            loc, loc + 1
        ));
    });
    return out;
}

function isTautology(w) {
    const kind = pg.nodeKind(w);
    if (kind === "A_Const") {
        const inner = pg.inner(w);
        const b = pg.asMap(inner, "boolval");
        if (b && b.boolval === true) return true;
        return false;
    }
    if (kind === "A_Expr") {
        const inner = pg.inner(w);
        if (pg.asString(inner, "kind") !== "AEXPR_OP") return false;
        const l = pg.asMap(inner, "lexpr");
        const r = pg.asMap(inner, "rexpr");
        const lk = pg.nodeKind(l);
        const rk = pg.nodeKind(r);
        if (lk !== "A_Const" || rk !== "A_Const") return false;
        // Compare ival/sval for equality.
        const li = pg.inner(l);
        const ri = pg.inner(r);
        const liv = pg.asMap(li, "ival");
        const riv = pg.asMap(ri, "ival");
        if (liv && riv && pg.asInt(liv, "ival") === pg.asInt(riv, "ival")) return true;
        const lsv = pg.asMap(li, "sval");
        const rsv = pg.asMap(ri, "sval");
        if (lsv && rsv && pg.asString(lsv, "sval") === pg.asString(rsv, "sval")) return true;
    }
    return false;
}
