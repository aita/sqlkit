-- PostgreSQL window-function statements.

SELECT id, row_number() OVER (PARTITION BY user_id ORDER BY id DESC) FROM posts;
SELECT id, row_number() OVER (ORDER BY id) FROM posts;
SELECT id, rank() OVER (PARTITION BY user_id ORDER BY score DESC) FROM posts;
SELECT id, dense_rank() OVER (PARTITION BY user_id ORDER BY score DESC) FROM posts;
SELECT id, lag(score) OVER (PARTITION BY user_id ORDER BY id) FROM posts;
SELECT id, lead(score) OVER (PARTITION BY user_id ORDER BY id) FROM posts;
SELECT id, sum(amount) OVER (PARTITION BY user_id) FROM orders;
SELECT id, count(*) OVER () FROM posts;
SELECT id, row_number() OVER w FROM posts WINDOW w AS (PARTITION BY user_id ORDER BY id);
SELECT id, rank() OVER w, dense_rank() OVER w FROM posts WINDOW w AS (PARTITION BY user_id ORDER BY score DESC);
