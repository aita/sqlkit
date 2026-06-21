-- Portable JOIN statements.

SELECT posts.id, users.name FROM posts JOIN users ON posts.user_id = users.id;
SELECT posts.id FROM posts LEFT JOIN users ON posts.user_id = users.id;
SELECT posts.id FROM posts INNER JOIN users ON posts.user_id = users.id WHERE users.name = 'alice';
SELECT a.id FROM users a JOIN posts b ON b.user_id = a.id JOIN comments c ON c.post_id = b.id;
SELECT posts.id FROM posts RIGHT JOIN users ON posts.user_id = users.id;
SELECT posts.id FROM posts LEFT OUTER JOIN users ON posts.user_id = users.id;
SELECT posts.id FROM posts RIGHT OUTER JOIN users ON posts.user_id = users.id;
SELECT u.id FROM users u CROSS JOIN posts p;
SELECT u.id, p.id FROM users u JOIN posts p USING (id);
SELECT u.id FROM users u NATURAL JOIN posts p;
SELECT u.id FROM users u LEFT JOIN posts p ON p.user_id = u.id LEFT JOIN comments c ON c.post_id = p.id;
SELECT a.id, b.id FROM users a JOIN users b ON a.manager_id = b.id;
SELECT a.id FROM users a JOIN posts b ON b.user_id = a.id AND b.published = true;
