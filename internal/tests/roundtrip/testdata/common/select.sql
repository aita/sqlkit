-- Portable SELECT statements: parsed and rendered for both PostgreSQL and MySQL.

SELECT id FROM users;
SELECT id, name FROM users;
SELECT id, name, email FROM users WHERE id = 1;
SELECT id FROM users WHERE name = 'alice';
SELECT id FROM users WHERE age >= 18 AND age <= 65;
SELECT id FROM users WHERE age > 18 OR name = 'bob';
SELECT id FROM users WHERE id IN (1, 2, 3);
SELECT id FROM users WHERE id NOT IN (4, 5, 6);
SELECT id FROM users WHERE name IS NULL;
SELECT id FROM users WHERE name IS NOT NULL;
SELECT id FROM users WHERE id BETWEEN 1 AND 10;
SELECT id FROM users WHERE name LIKE 'a%';
SELECT id FROM users WHERE NOT (id = 1);
SELECT id FROM users ORDER BY id;
SELECT id FROM users ORDER BY id DESC;
SELECT id FROM users ORDER BY created_at DESC, id ASC;
SELECT id FROM users LIMIT 10;
SELECT DISTINCT name FROM users;
SELECT count(*) FROM users;
SELECT min(age), max(age) FROM users;
SELECT user_id, count(*) FROM posts GROUP BY user_id;
SELECT user_id, count(*) FROM posts GROUP BY user_id HAVING count(*) > 1;
