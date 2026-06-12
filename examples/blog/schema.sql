-- Schema for the blog example. Apply with psql, then regenerate bindings:
--
--   psql "$DATABASE_URL" -f schema.sql
--   go run github.com/sqlkit-dev/sqlkit/cmd/sqlkitgen \
--     -dsn "$DATABASE_URL" -schema public -package blogdb -out blogdb/tables.go

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
