-- PostgreSQL-specific statements: boolean literals, ILIKE, RETURNING, CTEs,
-- window functions, row locking, and ON CONFLICT upserts.

SELECT id FROM users WHERE active = true;
SELECT id FROM users WHERE active = false;
SELECT id FROM users WHERE name ILIKE 'a%';
SELECT id, email FROM users WHERE id > 10 ORDER BY id ASC;
SELECT id FROM users FOR UPDATE;
SELECT id FROM users FOR UPDATE SKIP LOCKED;
SELECT id FROM users FOR SHARE;
WITH active AS (SELECT id FROM users WHERE active) SELECT id FROM active;
SELECT id, row_number() OVER (PARTITION BY user_id ORDER BY id DESC) FROM posts;
