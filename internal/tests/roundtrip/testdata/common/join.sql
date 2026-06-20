-- Portable JOIN statements.

SELECT posts.id, users.name FROM posts JOIN users ON posts.user_id = users.id;
SELECT posts.id FROM posts LEFT JOIN users ON posts.user_id = users.id;
SELECT posts.id FROM posts INNER JOIN users ON posts.user_id = users.id WHERE users.name = 'alice';
SELECT a.id FROM users a JOIN posts b ON b.user_id = a.id JOIN comments c ON c.post_id = b.id;
