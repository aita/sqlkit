-- PostgreSQL common-table-expression statements.

WITH active AS (SELECT id FROM users WHERE active) SELECT id FROM active;
WITH recent AS (SELECT id, user_id FROM posts WHERE created_at > 100) SELECT user_id FROM recent;
WITH a AS (SELECT id FROM users), b AS (SELECT id FROM admins) SELECT a.id FROM a JOIN b ON a.id = b.id;
WITH counts AS (SELECT user_id, count(*) AS n FROM posts GROUP BY user_id) SELECT user_id FROM counts WHERE n > 5;
WITH RECURSIVE nums AS (SELECT 1 AS n UNION ALL SELECT n + 1 FROM nums WHERE n < 10) SELECT n FROM nums;
WITH filtered AS (SELECT id FROM users WHERE active = true) SELECT id FROM filtered ORDER BY id LIMIT 5;
