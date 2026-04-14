-- expect: (none) — SELECT * inside EXISTS is idiomatic and has zero cost
SELECT u.id FROM users u
 WHERE EXISTS (SELECT * FROM orders o WHERE o.user_id = u.id);
