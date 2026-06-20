-- PostgreSQL-specific joins (FULL [OUTER] JOIN is not accepted by the MySQL frontend).

SELECT posts.id FROM posts FULL JOIN users ON posts.user_id = users.id;
SELECT posts.id FROM posts FULL OUTER JOIN users ON posts.user_id = users.id;
