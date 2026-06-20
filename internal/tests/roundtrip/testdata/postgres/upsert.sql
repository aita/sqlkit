-- PostgreSQL ON CONFLICT upserts and RETURNING.

INSERT INTO users (id) VALUES (1) ON CONFLICT (id) DO NOTHING;
INSERT INTO users (id, name) VALUES (1, 'a') ON CONFLICT (id) DO UPDATE SET name = excluded.name;
INSERT INTO users (id, name) VALUES (1, 'a') ON CONFLICT (id) DO UPDATE SET name = excluded.name WHERE users.active = true;
INSERT INTO users (id, name, age) VALUES (1, 'a', 20) ON CONFLICT (id) DO UPDATE SET name = excluded.name, age = excluded.age;
INSERT INTO users (id, name) VALUES (1, 'a') ON CONFLICT ON CONSTRAINT users_pkey DO NOTHING;
INSERT INTO users (id, name) VALUES (1, 'a') ON CONFLICT ON CONSTRAINT users_pkey DO UPDATE SET name = excluded.name;
INSERT INTO users (id, name) VALUES (1, 'a') ON CONFLICT (id) DO UPDATE SET name = excluded.name RETURNING id;
INSERT INTO users (id, name) VALUES (1, 'a') RETURNING id;
INSERT INTO users (id, name) VALUES (1, 'a') RETURNING id, name;
UPDATE users SET name = 'x' WHERE id = 5 RETURNING id, name;
UPDATE users SET name = 'x', age = age + 1 WHERE id = 5 RETURNING id;
DELETE FROM posts WHERE id = 9 RETURNING id;
DELETE FROM posts WHERE user_id = 1 RETURNING id, user_id;
