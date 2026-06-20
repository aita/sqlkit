-- Portable aggregate, grouping and CASE expressions.

SELECT count(*) FROM posts;
SELECT count(id) FROM posts;
SELECT count(DISTINCT user_id) FROM posts;
SELECT sum(amount) FROM orders;
SELECT avg(amount) FROM orders;
SELECT min(amount), max(amount) FROM orders;
SELECT user_id, count(*) FROM posts GROUP BY user_id;
SELECT user_id, status, count(*) FROM posts GROUP BY user_id, status;
SELECT user_id, count(*) FROM posts GROUP BY user_id HAVING count(*) > 5;
SELECT user_id, sum(amount) FROM orders GROUP BY user_id HAVING sum(amount) > 100;
SELECT id, CASE WHEN age >= 18 THEN 'adult' ELSE 'minor' END FROM users;
SELECT id, CASE WHEN age < 13 THEN 'child' WHEN age < 18 THEN 'teen' ELSE 'adult' END FROM users;
SELECT id, CASE status WHEN 1 THEN 'active' WHEN 0 THEN 'inactive' ELSE 'unknown' END FROM users;
SELECT user_id, count(*) FROM posts WHERE published = true GROUP BY user_id ORDER BY count(*) DESC;
