-- PostgreSQL-specific writes: ON CONFLICT upserts and RETURNING.

INSERT INTO users (id) VALUES (1) ON CONFLICT (id) DO NOTHING;
INSERT INTO users (id, name) VALUES (1, 'a') ON CONFLICT (id) DO UPDATE SET name = excluded.name;
INSERT INTO users (id, name) VALUES (1, 'a') ON CONFLICT (id) DO UPDATE SET name = excluded.name RETURNING id;
INSERT INTO users (id, name) VALUES (1, 'a') RETURNING id;
UPDATE users SET name = 'x' WHERE id = 5 RETURNING id, name;
DELETE FROM posts WHERE id = 9 RETURNING id;
