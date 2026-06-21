-- Portable set-operation statements: UNION, INTERSECT, EXCEPT.

SELECT id FROM users UNION SELECT id FROM admins;
SELECT id FROM users UNION ALL SELECT id FROM admins;
SELECT id FROM users INTERSECT SELECT id FROM admins;
SELECT id FROM users EXCEPT SELECT id FROM admins;
SELECT id FROM users UNION SELECT id FROM admins ORDER BY id;
SELECT id FROM users UNION ALL SELECT id FROM admins ORDER BY id DESC;
SELECT id FROM users UNION SELECT id FROM admins UNION SELECT id FROM guests;
SELECT id FROM a UNION ALL SELECT id FROM b UNION ALL SELECT id FROM c;

-- Grouping regression: a nested set operation carrying its own ORDER BY/LIMIT
-- must stay parenthesized even when it shares the outer operator, or the clauses
-- re-scope to the whole chain and the rendering becomes invalid SQL (an ORDER BY
-- mid-UNION-chain). See sql.compileOperand.
(SELECT id FROM users UNION SELECT id FROM admins ORDER BY id LIMIT 3) UNION SELECT id FROM guests;
-- Mixed-operator precedence: explicit parentheses must survive a round-trip,
-- since INTERSECT binds tighter than UNION.
(SELECT id FROM a UNION SELECT id FROM b) INTERSECT SELECT id FROM c;
SELECT id FROM a UNION (SELECT id FROM b INTERSECT SELECT id FROM c);
