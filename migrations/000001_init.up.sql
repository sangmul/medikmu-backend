-- Foundation schema. Every tenant-owned table carries facility_id (see docs/architecture.md).
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE facilities (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text        NOT NULL,
    kind       text        NOT NULL CHECK (kind IN ('solo_doctor', 'clinic', 'hospital')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text        NOT NULL UNIQUE,
    phone         text        UNIQUE,
    password_hash text        NOT NULL,
    full_name     text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- A user can be a patient and/or staff of one or more facilities.
CREATE TABLE memberships (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    facility_id uuid        NOT NULL REFERENCES facilities (id) ON DELETE CASCADE,
    role        text        NOT NULL CHECK (role IN ('admin', 'doctor', 'nurse', 'pharmacist', 'finance', 'staff')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, facility_id, role)
);

CREATE TABLE audit_logs (
    id          bigserial PRIMARY KEY,
    facility_id uuid,
    actor_id    uuid,
    action      text        NOT NULL,
    resource    text        NOT NULL,
    resource_id text,
    created_at  timestamptz NOT NULL DEFAULT now()
);
