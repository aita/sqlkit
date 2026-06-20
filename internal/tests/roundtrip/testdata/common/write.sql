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
