-- Demo: missing_index (name no tiene índice en users).
SELECT id, email FROM users WHERE name = 'User 42';
