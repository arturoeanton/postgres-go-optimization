// js_distinct_in_exists — DISTINCT inside EXISTS / IN subquery.
//
// EXISTS and IN subqueries ask a yes/no question per outer row, so the
// cardinality of the inner query beyond "any row" is irrelevant. The
// optimizer in subselect.c:convert_EXISTS_sublink_to_join already prunes
// duplicates implicitly, and DISTINCT inside the sublink just forces a
// pointless sort or hash aggregate before the rewrite fires.
function check(ctx) {
    const out = [];
    pg.findNodes(ctx.ast, "SubLink").forEach(function (sl) {
        const t = pg.asString(sl, "subLinkType");
        if (t !== "EXISTS_SUBLINK" && t !== "ANY_SUBLINK" && t !== "ALL_SUBLINK") return;
        const sub = pg.asMap(sl, "subselect");
        const inner = pg.inner(sub);
        if (!inner) return;
        const distinct = pg.asList(inner, "distinctClause");
        if (!distinct || distinct.length === 0) return;
        const loc = pg.firstLocation(sub);
        out.push(pg.finding(
            "DISTINCT inside a subquery used with EXISTS/IN/ANY/ALL is redundant",
            "Remove the DISTINCT — the semi-join rewrite already deduplicates.",
            loc, loc + 1
        ));
    });
    return out;
}
