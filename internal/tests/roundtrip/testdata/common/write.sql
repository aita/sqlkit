-- Portable INSERT / UPDATE / DELETE statements.

INSERT INTO users (id, name) VALUES (1, 'a');
INSERT INTO users (id, name) VALUES (1, 'a'), (2, 'b');
INSERT INTO users (id, name, email) VALUES (1, 'a', 'a@example.com');
INSERT INTO archive (id) SELECT id FROM users WHERE name = 'old';
UPDATE users SET name = 'x' WHERE id = 5;
UPDATE users SET name = 'x', age = 30 WHERE id = 5;
UPDATE counters SET n = n + 1 WHERE id = 1;
DELETE FROM posts WHERE id = 9;
DELETE FROM posts WHERE user_id = 1 AND id = 2;
INSERT INTO users (id, name) VALUES (1, 'a'), (2, 'b'), (3, 'c');
INSERT INTO archive (id, name) SELECT id, name FROM users WHERE active = false;
UPDATE counters SET n = n + 1, updated = true WHERE id = 1;
UPDATE users SET name = 'x', age = age + 1 WHERE id = 5;
UPDATE users SET active = false WHERE last_login < 100;
DELETE FROM sessions WHERE expires_at < 1000;
DELETE FROM posts WHERE user_id IN (SELECT id FROM users WHERE active = false);
UPDATE posts SET published = true WHERE user_id = 1 AND published = false;
