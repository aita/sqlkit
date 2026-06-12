-- Schema for the blog example. Apply with psql, then regenerate bindings:
--
--   psql "$DATABASE_URL" -f schema.sql
--   go run github.com/aita/sqlkit/cmd/sqlkitgen \
--     -dsn "$DATABASE_URL" -schema public -package blogdb -out blogdb/tables.go
--
-- The same schema is also declared in code in design/design.go (see the dsl
-- package); `go run ./gen` regenerates blogdb from it without a database.

CREATE TABLE users (
    id         bigserial PRIMARY KEY,
    email      varchar(255) NOT NULL UNIQUE,
    name       text,
    created_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE users IS 'Registered blog authors';
COMMENT ON COLUMN users.email IS 'Unique login address';

CREATE TABLE posts (
    id        bigserial PRIMARY KEY,
    user_id   bigint NOT NULL REFERENCES users (id),
    title     text NOT NULL CHECK (title <> ''),
    body      text,
    published boolean NOT NULL DEFAULT false
);

CREATE INDEX idx_posts_user_id ON posts (user_id);
