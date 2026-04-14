// js_string_concat_in_where — col || 'x' = 'foo' in WHERE.
//
// match_clause_to_indexcol in path/indxpath.c matches a clause to an
// index column only when the column appears bare (or in a form the
// index expression covers). A concatenation of the column with a
// literal produces a non-sargable predicate and forces a sequential
// scan even when a btree on the column exists.
function check(ctx) {
    const out = [];
    pg.forEachSelect(ctx.ast, function (sel) {
        const w = pg.asMap(sel, "whereClause");
        if (!w) return;
        pg.walk(w, function (_, node) {
            if (pg.nodeKind(node) !== "A_Expr") return true;
            const inner = pg.inner(node);
            if (pg.asString(inner, "kind") !== "AEXPR_OP") return true;
            const opList = pg.asList(inner, "name") || [];
            if (opList.length === 0) return true;
            const op = pg.asString(pg.asMap(opList[0], "String"), "sval");
            if (op !== "=" && op !== "<>") return true;
            // Either side is itself an A_Expr with || operator referencing a column.
            ["lexpr", "rexpr"].forEach(function (side) {
                const s = pg.asMap(inner, side);
                if (!s) return;
                if (pg.nodeKind(s) !== "A_Expr") return;
                const si = pg.inner(s);
                const sn = pg.asList(si, "name") || [];
                if (sn.length === 0) return;
                const sop = pg.asString(pg.asMap(sn[0], "String"), "sval");
                if (sop !== "||") return;
                // Confirm one operand of the concat is a column reference.
                const colOnEither =
                    pg.nodeKind(pg.asMap(si, "lexpr")) === "ColumnRef" ||
                    pg.nodeKind(pg.asMap(si, "rexpr")) === "ColumnRef";
                if (!colOnEither) return;
                const loc = pg.asInt(inner, "location");
                out.push(pg.finding(
                    "Concatenation on a column in WHERE is not sargable and blocks btree index matches",
                    "Rewrite as a range predicate on the bare column, e.g. col LIKE 'prefix%' or col = 'exact'.",
                    loc, loc + 1
                ));
            });
            return true;
        });
    });
    return out;
}
