-- expect: offset_pagination
SELECT id, email FROM users ORDER BY id LIMIT 50 OFFSET 100000;
