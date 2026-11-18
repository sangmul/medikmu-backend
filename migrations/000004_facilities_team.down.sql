DROP TABLE IF EXISTS invites;
DROP INDEX IF EXISTS memberships_user_facility_uidx;
ALTER TABLE facilities
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS created_by,
    DROP COLUMN IF EXISTS address,
    DROP COLUMN IF EXISTS phone;
