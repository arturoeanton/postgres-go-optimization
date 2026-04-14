-- expect: js_distinct_in_exists
SELECT u.id FROM users u WHERE EXISTS (SELECT DISTINCT o.user_id FROM orders o WHERE o.user_id = u.id);
