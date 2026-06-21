-- MySQL-specific statements: LIMIT with ORDER BY and back-tick rendering.

SELECT id FROM users WHERE name LIKE 'a%';
SELECT id, name FROM users ORDER BY id DESC LIMIT 10;
SELECT id FROM users u JOIN posts p ON p.user_id = u.id;
SELECT id FROM users WHERE id IN (1, 2, 3) ORDER BY id;
SELECT id FROM users LIMIT 10 OFFSET 20;
SELECT id FROM users LIMIT 5, 10;
SELECT id, name FROM users ORDER BY name ASC, id DESC LIMIT 25;
SELECT id FROM users ORDER BY created_at DESC LIMIT 100;
SELECT user_id, count(*) FROM posts GROUP BY user_id HAVING count(*) > 1 ORDER BY user_id;
SELECT id FROM users u LEFT JOIN posts p ON p.user_id = u.id WHERE p.id IS NULL;
