-- Portable subquery / set-operation statements.

SELECT id FROM users WHERE EXISTS (SELECT 1 FROM posts WHERE posts.user_id = users.id);
SELECT id FROM users WHERE NOT EXISTS (SELECT 1 FROM posts WHERE posts.user_id = users.id);
SELECT id FROM users WHERE id IN (SELECT user_id FROM posts);
SELECT id FROM users UNION SELECT id FROM admins;
SELECT id FROM users UNION ALL SELECT id FROM admins;
