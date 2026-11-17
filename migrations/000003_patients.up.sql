-- Patients are owned by one facility (tenant). Every query MUST filter by facility_id.
CREATE TABLE patients (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    facility_id       uuid        NOT NULL REFERENCES facilities (id) ON DELETE RESTRICT,
    mrn               text        NOT NULL,               -- medical record number, per facility (RM-000001)
    full_name         text        NOT NULL,
    sex               text        NOT NULL CHECK (sex IN ('F', 'M')),
    birth_date        date        NOT NULL,
    birth_place       text        NOT NULL DEFAULT '',
    nik               text,                               -- 16 digits; SATUSEHAT identifies people by it
    guardian_name     text        NOT NULL DEFAULT '',    -- required by the API for patients under 18
    guardian_relation text        NOT NULL DEFAULT '',
    phone             text        NOT NULL,
    email             text,
    address           text        NOT NULL DEFAULT '',
    payment_type      text        NOT NULL CHECK (payment_type IN ('bpjs', 'private', 'insurance')),
    bpjs_number       text,                               -- 13 digits
    allergies         text[]      NOT NULL DEFAULT '{}',
    conditions        text[]      NOT NULL DEFAULT '{}',
    notes             text        NOT NULL DEFAULT '',
    user_id           uuid        REFERENCES users (id) ON DELETE SET NULL, -- linked patient app account (phase 3)
    consent_at        timestamptz NOT NULL,
    created_by        uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    archived_at       timestamptz,
    UNIQUE (facility_id, mrn)
);

-- One active patient per NIK per facility.
CREATE UNIQUE INDEX patients_facility_nik_uidx ON patients (facility_id, nik)
    WHERE nik IS NOT NULL AND archived_at IS NULL;

-- Keyset pagination: newest first.
CREATE INDEX patients_facility_created_idx ON patients (facility_id, created_at DESC, id DESC)
    WHERE archived_at IS NULL;

CREATE INDEX patients_facility_name_idx ON patients (facility_id, lower(full_name));

-- Race-free MRN generation: one counter row per facility.
CREATE TABLE patient_counters (
    facility_id uuid PRIMARY KEY REFERENCES facilities (id) ON DELETE CASCADE,
    last_value  bigint NOT NULL DEFAULT 0
);
