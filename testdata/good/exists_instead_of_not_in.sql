-- expect: (none)
SELECT id FROM users u
 WHERE NOT EXISTS (SELECT 1 FROM banned_users b WHERE b.user_id = u.id);
