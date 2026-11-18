-- Facility profile, one role per user per facility, and staff invites.
ALTER TABLE facilities
    ADD COLUMN phone      text        NOT NULL DEFAULT '',
    ADD COLUMN address    text        NOT NULL DEFAULT '',
    ADD COLUMN created_by uuid        REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

-- A user holds exactly one role in a facility (the original UNIQUE allowed several).
CREATE UNIQUE INDEX memberships_user_facility_uidx ON memberships (user_id, facility_id);

-- Staff invites. Only the SHA-256 hash of the token is stored; the token is shown once to the inviting admin.
CREATE TABLE invites (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    facility_id uuid        NOT NULL REFERENCES facilities (id) ON DELETE CASCADE,
    email       text        NOT NULL, -- lowercased
    role        text        NOT NULL CHECK (role IN ('admin', 'doctor', 'nurse', 'pharmacist', 'finance', 'staff')),
    token_hash  text        NOT NULL UNIQUE,
    invited_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
    expires_at  timestamptz NOT NULL,
    accepted_at timestamptz,
    revoked_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- One pending invite per email per facility.
CREATE UNIQUE INDEX invites_pending_uidx ON invites (facility_id, email)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;
