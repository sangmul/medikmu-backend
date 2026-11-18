-- Phone verification state (OTP delivery is not built yet; stays NULL until it is).
ALTER TABLE users ADD COLUMN phone_verified_at timestamptz;

-- Family members belong to a patient app account (user_id), not to a facility: this is the patient's own data.
CREATE TABLE family_members (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    full_name        text        NOT NULL,
    relation         text        NOT NULL CHECK (relation IN ('self', 'spouse', 'child', 'parent', 'sibling', 'other')),
    sex              text        NOT NULL CHECK (sex IN ('F', 'M')),
    birth_date       date        NOT NULL,
    nik              text,                -- 16 digits
    bpjs_number      text,                -- 13 digits
    bpjs_holder_name text        NOT NULL DEFAULT '',
    bpjs_class       smallint    CHECK (bpjs_class BETWEEN 1 AND 3),
    avatar_tone      text        NOT NULL DEFAULT 'purple' CHECK (avatar_tone IN ('purple', 'blue', 'orange', 'teal')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX family_members_user_idx ON family_members (user_id, created_at, id);

-- A NIK identifies one person: at most once per account.
CREATE UNIQUE INDEX family_members_user_nik_uidx ON family_members (user_id, nik) WHERE nik IS NOT NULL;
