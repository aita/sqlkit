-- PostgreSQL keyword-syntax functions. libpg_query reports these as ordinary
-- pg_catalog calls tagged COERCE_SQL_SYNTAX; the parser must restore the keyword
-- form, or e.g. EXTRACT renders as extract(field, x), which the grammar rejects.

SELECT EXTRACT(YEAR FROM d) FROM t;
SELECT EXTRACT(EPOCH FROM now()) FROM t;
SELECT POSITION('a' IN s) FROM t;
-- SUBSTRING / TRIM stay ordinary calls (substring(s, 1, 3), btrim(s, ' ')), which
-- are valid SQL, so they round-trip without a dedicated node.
SELECT SUBSTRING(s FROM 1 FOR 3) FROM t;
SELECT TRIM(BOTH ' ' FROM s) FROM t;
