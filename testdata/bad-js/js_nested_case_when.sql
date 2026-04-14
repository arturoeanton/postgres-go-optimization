-- expect: js_nested_case_when
SELECT
  CASE
    WHEN a = 1 THEN
      CASE WHEN b = 1 THEN
        CASE WHEN c = 1 THEN 'deep' ELSE 'med' END
      ELSE 'shallow' END
    ELSE 'none'
  END
FROM t;
