-- Portable subquery statements: EXISTS, IN, scalar, derived tables, correlated.

SELECT id FROM users WHERE EXISTS (SELECT 1 FROM posts WHERE posts.user_id = users.id);
SELECT id FROM users WHERE NOT EXISTS (SELECT 1 FROM posts WHERE posts.user_id = users.id);
SELECT id FROM users WHERE id IN (SELECT user_id FROM posts);
SELECT id FROM users WHERE id NOT IN (SELECT user_id FROM banned);
SELECT id FROM users WHERE id IN (SELECT user_id FROM posts WHERE published = true);
SELECT id, (SELECT count(*) FROM posts WHERE posts.user_id = users.id) FROM users;
SELECT x.id FROM (SELECT id FROM users WHERE active = true) AS x;
SELECT x.id, x.name FROM (SELECT id, name FROM users) AS x WHERE x.id > 10;
SELECT id FROM users u WHERE EXISTS (SELECT 1 FROM posts p WHERE p.user_id = u.id AND p.published = true);
SELECT id FROM users u WHERE (SELECT count(*) FROM posts p WHERE p.user_id = u.id) > 5;
SELECT id FROM orders WHERE amount > (SELECT avg(amount) FROM orders);
