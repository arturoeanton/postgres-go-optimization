-- expect: function_on_column
SELECT id FROM users WHERE lower(email) = 'alice@example.com';
