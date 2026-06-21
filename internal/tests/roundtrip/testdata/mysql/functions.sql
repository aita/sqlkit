-- MySQL keyword-syntax functions. EXTRACT(unit FROM x) and POSITION(sub IN str)
-- reach the parser as ordinary calls; the parser must restore the keyword form,
-- or EXTRACT(unit, x) / POSITION(sub, str) are rejected by MySQL's grammar.

SELECT EXTRACT(YEAR FROM d) FROM t;
SELECT POSITION('a' IN s) FROM t;
-- SUBSTRING keeps the ordinary call form SUBSTRING(s, 1, 3), which is valid.
SELECT SUBSTRING(s FROM 1 FOR 3) FROM t;
-- CONVERT(x USING charset) and TRIM(... FROM x) cannot render as generic calls,
-- so they fall back to a raw passthrough that keeps the keyword syntax.
SELECT CONVERT(a USING utf8) FROM t;
SELECT TRIM(' ' FROM a) FROM t;
