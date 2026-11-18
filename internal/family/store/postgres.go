// Package store is the PostgreSQL implementation of family's domain.Store.
// Every query filters by user_id: that is the ownership boundary.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sangmul/medikmu-backend/internal/family/domain"
)

// Postgres implements domain.Store on a pgx pool.
type Postgres struct{ pool *pgxpool.Pool }

var _ domain.Store = (*Postgres)(nil)

// New returns a Postgres-backed store.
func New(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

const (
	pgUniqueViolation = "23505"
	nikIndex          = "family_members_user_nik_uidx"
)

const cols = `id, user_id, full_name, relation, sex, birth_date, COALESCE(nik, ''), avatar_tone,
	COALESCE(bpjs_number, ''), bpjs_holder_name, COALESCE(bpjs_class, 0), created_at, updated_at`

func scan(row pgx.Row) (domain.Member, error) {
	var m domain.Member
	err := row.Scan(&m.ID, &m.UserID, &m.FullName, &m.Relation, &m.Sex, &m.BirthDate, &m.NIK, &m.AvatarTone,
		&m.BPJSNumber, &m.BPJSHolderName, &m.BPJSClass, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Member{}, domain.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == nikIndex {
		return domain.Member{}, domain.ErrNIKTaken
	}
	if err != nil {
		return domain.Member{}, fmt.Errorf("family member query: %w", err)
	}
	return m, nil
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullableInt(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

func (s *Postgres) List(ctx context.Context, userID string) ([]domain.Member, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM family_members WHERE user_id = $1 ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list family members: %w", err)
	}
	defer rows.Close()
	out := []domain.Member{}
	for rows.Next() {
		m, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Postgres) Get(ctx context.Context, userID, id string) (domain.Member, error) {
	return scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM family_members WHERE id = $1 AND user_id = $2`, id, userID))
}

// Create enforces the per-account cap under a per-user advisory lock, so concurrent creates cannot exceed it.
func (s *Postgres) Create(ctx context.Context, userID string, m domain.Member) (domain.Member, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Member{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "family-create:"+userID); err != nil {
		return domain.Member{}, fmt.Errorf("lock: %w", err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM family_members WHERE user_id = $1`, userID).Scan(&n); err != nil {
		return domain.Member{}, fmt.Errorf("count: %w", err)
	}
	if n >= domain.MaxMembersPerUser {
		return domain.Member{}, domain.ErrLimitReached
	}
	created, err := scan(tx.QueryRow(ctx, `
		INSERT INTO family_members (user_id, full_name, relation, sex, birth_date, nik, avatar_tone,
			bpjs_number, bpjs_holder_name, bpjs_class)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING `+cols,
		userID, m.FullName, m.Relation, m.Sex, m.BirthDate, nullable(m.NIK), m.AvatarTone,
		nullable(m.BPJSNumber), m.BPJSHolderName, nullableInt(m.BPJSClass)))
	if err != nil {
		return domain.Member{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Member{}, fmt.Errorf("commit: %w", err)
	}
	return created, nil
}

func (s *Postgres) Update(ctx context.Context, userID string, m domain.Member) (domain.Member, error) {
	return scan(s.pool.QueryRow(ctx, `
		UPDATE family_members SET full_name = $3, relation = $4, sex = $5, birth_date = $6, nik = $7,
			avatar_tone = $8, bpjs_number = $9, bpjs_holder_name = $10, bpjs_class = $11, updated_at = now()
		WHERE id = $1 AND user_id = $2 RETURNING `+cols,
		m.ID, userID, m.FullName, m.Relation, m.Sex, m.BirthDate, nullable(m.NIK), m.AvatarTone,
		nullable(m.BPJSNumber), m.BPJSHolderName, nullableInt(m.BPJSClass)))
}

func (s *Postgres) SetNIK(ctx context.Context, userID, id, nik string) (domain.Member, error) {
	return scan(s.pool.QueryRow(ctx,
		`UPDATE family_members SET nik = $3, updated_at = now() WHERE id = $1 AND user_id = $2 RETURNING `+cols,
		id, userID, nullable(nik)))
}

func (s *Postgres) Delete(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM family_members WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete family member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
