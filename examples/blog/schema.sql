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

CREATE TABLE posts (
    id        bigserial PRIMARY KEY,
    user_id   bigint NOT NULL REFERENCES users (id),
    title     text NOT NULL,
    body      text,
    published boolean NOT NULL DEFAULT false
);
