-- Generated from design/design.go by 'go run ./gen'. Apply with:
--
--   psql "$DATABASE_URL" -f schema.sql

CREATE TABLE "public"."users" (
    "id" bigserial,
    "email" varchar(255) NOT NULL,
    "name" text,
    "created_at" timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY ("id")
);

CREATE TABLE "public"."posts" (
    "id" bigserial,
    "user_id" bigint NOT NULL,
    "title" text NOT NULL,
    "body" text,
    "published" boolean NOT NULL DEFAULT false,
    PRIMARY KEY ("id"),
    CONSTRAINT "ck_posts_1" CHECK (title <> '')
);

CREATE UNIQUE INDEX "uq_users_email" ON "public"."users" ("email");

CREATE INDEX "idx_posts_user_id" ON "public"."posts" ("user_id");

ALTER TABLE "public"."posts" ADD CONSTRAINT "fk_posts_user_id" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id");

COMMENT ON TABLE "public"."users" IS 'Registered blog authors';
COMMENT ON COLUMN "public"."users"."email" IS 'Unique login address';
