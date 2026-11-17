-- Rotating refresh tokens. Only the SHA-256 hash is stored.
-- A "family" is one login session; reuse of an already-used token revokes the whole family.
CREATE TABLE refresh_tokens (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    family_id   uuid        NOT NULL,
    token_hash  text        NOT NULL UNIQUE,
    facility_id uuid        REFERENCES facilities (id) ON DELETE SET NULL,
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    revoked_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX refresh_tokens_family_idx ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_user_idx ON refresh_tokens (user_id);
CREATE INDEX refresh_tokens_expires_idx ON refresh_tokens (expires_at);
