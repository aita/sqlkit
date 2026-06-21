-- PostgreSQL array constructors, subscripts, and slices.

-- Subscripting a column renders bare; subscripting an array constructor must be
-- parenthesized, or "ARRAY[1,2,3][1]" is invalid SQL. See sql.compileSubscript.
SELECT (ARRAY[1, 2, 3])[1] FROM t;
SELECT tags[1] FROM t;
SELECT tags[1:2] FROM t;
-- A field access must be fully parenthesized before a subscript, or
-- (rec).tags[1] is no longer read as an index into the field.
SELECT ((rec).tags)[1] FROM t;
SELECT ARRAY[1, 2, 3] FROM t;
SELECT id FROM t WHERE c = ANY(ARRAY[1, 2, 3]);
SELECT id FROM t WHERE c > ALL(ARRAY[1, 2, 3]);
