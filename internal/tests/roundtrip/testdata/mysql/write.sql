-- MySQL-specific writes: ON DUPLICATE KEY UPDATE upserts.

INSERT INTO users (id, name) VALUES (1, 'a') ON DUPLICATE KEY UPDATE name = VALUES(name);
INSERT INTO users (id, name) VALUES (1, 'a'), (2, 'b') ON DUPLICATE KEY UPDATE name = VALUES(name);
INSERT INTO users (id, name, age) VALUES (1, 'a', 20) ON DUPLICATE KEY UPDATE name = VALUES(name), age = VALUES(age);
INSERT INTO counters (id, n) VALUES (1, 1) ON DUPLICATE KEY UPDATE n = n + 1;
INSERT INTO archive (id, name) SELECT id, name FROM users WHERE active = false;
UPDATE users SET name = 'x', age = age + 1 WHERE id = 5;
DELETE FROM posts WHERE user_id IN (SELECT id FROM users WHERE active = false);
