-- expect: not_in_null
SELECT id FROM users
 WHERE id NOT IN (SELECT user_id FROM banned_users);
