-- Schema for the redact example. The redact CLI applies this DDL, introspects
-- it, and masks the literals bound to the columns whose comment marks them PII.
--
-- A column is PII when its comment contains "pii" (case-insensitive); the rest
-- are left untouched. Mark a column with COMMENT ON COLUMN ... IS '... PII'.
DROP TABLE IF EXISTS users;

CREATE TABLE users (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email      text NOT NULL,
    full_name  text NOT NULL,
    phone      text,
    country    text NOT NULL DEFAULT 'JP',
    created_at timestamptz NOT NULL DEFAULT now()
);

-- PII columns: their comments carry the marker.
COMMENT ON COLUMN users.email IS 'login address (PII)';
COMMENT ON COLUMN users.full_name IS 'legal name — PII';
COMMENT ON COLUMN users.phone IS 'contact number, PII';

-- Not PII: no marker, so their literals are never masked.
COMMENT ON COLUMN users.country IS 'ISO country code';
