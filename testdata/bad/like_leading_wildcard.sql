-- expect: like_leading_wildcard
SELECT id, title FROM articles WHERE title LIKE '%postgres%';
