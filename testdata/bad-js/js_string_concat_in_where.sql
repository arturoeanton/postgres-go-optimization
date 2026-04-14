-- expect: js_string_concat_in_where
SELECT id FROM users WHERE first_name || ' ' || last_name = 'Jane Doe';
