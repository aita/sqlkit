-- PostgreSQL-specific joins (FULL [OUTER] JOIN is not accepted by the MySQL frontend).

SELECT posts.id FROM posts FULL JOIN users ON posts.user_id = users.id;
SELECT posts.id FROM posts FULL OUTER JOIN users ON posts.user_id = users.id;

-- The shape SelectQuery.Fold emits: every column schema-qualified, one LEFT JOIN
-- per hop, and a re-folded table joined under a quoted alias (a self-reference).
SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name", "public"."posts"."id", "public"."posts"."user_id", "public"."posts"."title", "public"."comments"."id", "public"."comments"."post_id", "public"."comments"."body" FROM "public"."users" LEFT JOIN "public"."posts" ON ("public"."posts"."user_id" = "public"."users"."id") LEFT JOIN "public"."comments" ON ("public"."comments"."post_id" = "public"."posts"."id");
SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name", "public"."posts"."id", "public"."posts"."user_id", "public"."posts"."title", "User"."id", "User"."email", "User"."name" FROM "public"."users" LEFT JOIN "public"."posts" ON ("public"."posts"."user_id" = "public"."users"."id") LEFT JOIN "public"."users" AS "User" ON ("User"."id" = "public"."posts"."user_id");
