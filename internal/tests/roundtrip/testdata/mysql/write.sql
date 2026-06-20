-- MySQL-specific writes: ON DUPLICATE KEY UPDATE upserts.

INSERT INTO users (id, name) VALUES (1, 'a') ON DUPLICATE KEY UPDATE name = VALUES(name);
INSERT INTO users (id, name) VALUES (1, 'a'), (2, 'b') ON DUPLICATE KEY UPDATE name = VALUES(name);
