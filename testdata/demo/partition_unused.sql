-- Demo: partition_key_unused (la tabla events está particionada por `ts`).
SELECT kind, count(*) FROM events WHERE kind = 'click' GROUP BY kind;
