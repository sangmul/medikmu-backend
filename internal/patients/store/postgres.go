// Package store is the PostgreSQL implementation of patients' domain.Store.
// Every query filters by facility_id: that is the tenant boundary.
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sangmul/medikmu-backend/internal/patients/domain"
)

// Postgres implements domain.Store on a pgx pool.
type Postgres struct{ pool *pgxpool.Pool }

var _ domain.Store = (*Postgres)(nil)

// New returns a Postgres-backed store.
func New(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

const (
	pgUniqueViolation = "23505"
	nikIndex          = "patients_facility_nik_uidx"
)

// cols must match scan() order. Nullable columns are COALESCEd so scanning stays simple.
const cols = `id, facility_id, mrn, full_name, sex, birth_date, birth_place,
	COALESCE(nik, ''), guardian_name, guardian_relation, phone, COALESCE(email, ''), address,
	payment_type, COALESCE(bpjs_number, ''), allergies, conditions, notes,
	COALESCE(user_id::text, ''), consent_at, COALESCE(created_by::text, ''), created_at, updated_at`

func scan(row pgx.Row) (domain.Patient, error) {
	var p domain.Patient
	err := row.Scan(&p.ID, &p.FacilityID, &p.MRN, &p.FullName, &p.Sex, &p.BirthDate, &p.BirthPlace,
		&p.NIK, &p.GuardianName, &p.GuardianRelation, &p.Phone, &p.Email, &p.Address,
		&p.PaymentType, &p.BPJSNumber, &p.Allergies, &p.Conditions, &p.Notes,
		&p.UserID, &p.ConsentAt, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return domain.Patient{}, err
	}
	if p.Allergies == nil {
		p.Allergies = []string{}
	}
	if p.Conditions == nil {
		p.Conditions = []string{}
	}
	return p, nil
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func mapErr(err error, what string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == nikIndex {
		return domain.ErrNIKTaken
	}
	return fmt.Errorf("%s: %w", what, err)
}

// Create allocates the next MRN and inserts in one transaction, so MRNs are gap-free and race-free.
func (s *Postgres) Create(ctx context.Context, facilityID string, p domain.Patient) (domain.Patient, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Patient{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var n int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO patient_counters (facility_id, last_value) VALUES ($1, 1)
		ON CONFLICT (facility_id) DO UPDATE SET last_value = patient_counters.last_value + 1
		RETURNING last_value`, facilityID).Scan(&n); err != nil {
		return domain.Patient{}, fmt.Errorf("next mrn: %w", err)
	}

	created, err := scan(tx.QueryRow(ctx, `
		INSERT INTO patients (facility_id, mrn, full_name, sex, birth_date, birth_place, nik,
			guardian_name, guardian_relation, phone, email, address, payment_type, bpjs_number,
			allergies, conditions, notes, consent_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		RETURNING `+cols,
		facilityID, fmt.Sprintf("RM-%06d", n), p.FullName, p.Sex, p.BirthDate, p.BirthPlace, nullable(p.NIK),
		p.GuardianName, p.GuardianRelation, p.Phone, nullable(p.Email), p.Address, p.PaymentType, nullable(p.BPJSNumber),
		orEmpty(p.Allergies), orEmpty(p.Conditions), p.Notes, p.ConsentAt, nullable(p.CreatedBy)))
	if err != nil {
		return domain.Patient{}, mapErr(err, "insert patient")
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Patient{}, fmt.Errorf("commit: %w", err)
	}
	return created, nil
}

func (s *Postgres) Get(ctx context.Context, facilityID, id string) (domain.Patient, error) {
	p, err := scan(s.pool.QueryRow(ctx,
		`SELECT `+cols+` FROM patients WHERE id = $1 AND facility_id = $2 AND archived_at IS NULL`, id, facilityID))
	if err != nil {
		return domain.Patient{}, mapErr(err, "get patient")
	}
	return p, nil
}

func (s *Postgres) Update(ctx context.Context, facilityID string, p domain.Patient) (domain.Patient, error) {
	updated, err := scan(s.pool.QueryRow(ctx, `
		UPDATE patients SET full_name = $3, sex = $4, birth_date = $5, birth_place = $6, nik = $7,
			guardian_name = $8, guardian_relation = $9, phone = $10, email = $11, address = $12,
			payment_type = $13, bpjs_number = $14, allergies = $15, conditions = $16, notes = $17,
			updated_at = now()
		WHERE id = $1 AND facility_id = $2 AND archived_at IS NULL
		RETURNING `+cols,
		p.ID, facilityID, p.FullName, p.Sex, p.BirthDate, p.BirthPlace, nullable(p.NIK),
		p.GuardianName, p.GuardianRelation, p.Phone, nullable(p.Email), p.Address,
		p.PaymentType, nullable(p.BPJSNumber), orEmpty(p.Allergies), orEmpty(p.Conditions), p.Notes))
	if err != nil {
		return domain.Patient{}, mapErr(err, "update patient")
	}
	return updated, nil
}

func (s *Postgres) Archive(ctx context.Context, facilityID, id string, at time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE patients SET archived_at = $3 WHERE id = $1 AND facility_id = $2 AND archived_at IS NULL`,
		id, facilityID, at)
	if err != nil {
		return fmt.Errorf("archive patient: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// List is keyset-paginated (created_at DESC, id DESC). Search matches name (contains), MRN (prefix)
// or phone (contains); LIKE wildcards in the query are escaped.
func (s *Postgres) List(ctx context.Context, facilityID string, f domain.ListFilter) ([]domain.Patient, error) {
	var curAt *time.Time
	var curID *string
	if f.Cursor != nil {
		curAt, curID = &f.Cursor.CreatedAt, &f.Cursor.ID
	}
	q := likeEscape.Replace(f.Query)

	rows, err := s.pool.Query(ctx, `
		SELECT `+cols+` FROM patients
		WHERE facility_id = $1 AND archived_at IS NULL
		  AND ($2 = '' OR full_name ILIKE '%' || $2 || '%' ESCAPE '\'
		              OR mrn ILIKE $2 || '%' ESCAPE '\'
		              OR phone LIKE '%' || $2 || '%' ESCAPE '\')
		  AND ($3::timestamptz IS NULL OR (created_at, id) < ($3::timestamptz, $4::uuid))
		ORDER BY created_at DESC, id DESC
		LIMIT $5`, facilityID, q, curAt, curID, f.Limit+1)
	if err != nil {
		return nil, fmt.Errorf("list patients: %w", err)
	}
	defer rows.Close()

	var out []domain.Patient
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan patient: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

var likeEscape = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
