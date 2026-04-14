-- expect: js_union_branch_order_by
(SELECT id FROM users ORDER BY id) UNION (SELECT id FROM orders);
