-- MySQL-specific statements: LIMIT with ORDER BY and back-tick rendering.

SELECT id FROM users WHERE name LIKE 'a%';
SELECT id, name FROM users ORDER BY id DESC LIMIT 10;
SELECT id FROM users u JOIN posts p ON p.user_id = u.id;
SELECT id FROM users WHERE id IN (1, 2, 3) ORDER BY id;
