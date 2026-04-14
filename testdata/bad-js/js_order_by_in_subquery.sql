-- expect: js_order_by_in_subquery
SELECT q.id FROM (SELECT id FROM users ORDER BY created_at) q;
