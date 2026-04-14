// js_count_literal — flags COUNT(<literal>) as COUNT(*) in disguise.
//
// The planner treats COUNT(1), COUNT(42), and COUNT('x') identically to
// COUNT(*) because the argument is known to be non-null at analysis time
// (parse_func.c:ParseFuncOrColumn promotes count-of-constant to the
// star aggregate). Writing COUNT(*) avoids implying that the argument
// matters and matches idiomatic SQL.
function check(ctx) {
    const out = [];
    pg.findNodes(ctx.ast, "FuncCall").forEach(function (fc) {
        const nameList = pg.asList(fc, "funcname") || [];
        if (nameList.length === 0) return;
        const last = nameList[nameList.length - 1];
        const fname = pg.asString(pg.asMap(last, "String"), "sval");
        if (fname.toLowerCase() !== "count") return;
        if (fc.agg_star) return; // already COUNT(*)
        const args = pg.asList(fc, "args") || [];
        if (args.length !== 1) return;
        const arg = args[0];
        // A literal constant has kind "A_Const" at the top level.
        if (pg.nodeKind(arg) !== "A_Const") return;
        const loc = pg.firstLocation(fc);
        out.push(pg.finding(
            "COUNT(<literal>) is identical to COUNT(*); prefer COUNT(*) for clarity",
            "Replace COUNT(1) with COUNT(*); the planner already recognizes them as equivalent.",
            loc, loc + 1
        ));
    });
    return out;
}
