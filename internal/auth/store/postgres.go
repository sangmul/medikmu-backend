// Package store is the PostgreSQL implementation of auth's domain.Store.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sangmul/medikmu-backend/internal/auth/domain"
)

// Postgres implements domain.Store on a pgx pool. All queries are parameterized.
type Postgres struct {
	pool *pgxpool.Pool
}

var _ domain.Store = (*Postgres)(nil)

// New returns a Postgres-backed store.
func New(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

const (
	pgUniqueViolation = "23505"
	emailConstraint   = "users_email_key"
	phoneConstraint   = "users_phone_key"
)

func (p *Postgres) CreateUser(ctx context.Context, u domain.User) (domain.User, error) {
	var phone *string
	if u.Phone != "" {
		phone = &u.Phone
	}
	err := p.pool.QueryRow(ctx, `
		INSERT INTO users (email, phone, password_hash, full_name)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`,
		u.Email, phone, u.PasswordHash, u.FullName,
	).Scan(&u.ID, &u.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			switch pgErr.ConstraintName {
			case emailConstraint:
				return domain.User{}, domain.ErrEmailTaken
			case phoneConstraint:
				return domain.User{}, domain.ErrPhoneTaken
			}
		}
		return domain.User{}, fmt.Errorf("insert user: %w", err)
	}
	return u, nil
}

func (p *Postgres) UserByEmail(ctx context.Context, email string) (domain.User, error) {
	return p.scanUser(p.pool.QueryRow(ctx, userSelect+` WHERE email = $1`, email))
}

func (p *Postgres) UserByID(ctx context.Context, id string) (domain.User, error) {
	return p.scanUser(p.pool.QueryRow(ctx, userSelect+` WHERE id = $1`, id))
}

const userSelect = `SELECT id, email, COALESCE(phone, ''), password_hash, full_name, created_at FROM users`

func (p *Postgres) scanUser(row pgx.Row) (domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.Email, &u.Phone, &u.PasswordHash, &u.FullName, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("query user: %w", err)
	}
	return u, nil
}

func (p *Postgres) Memberships(ctx context.Context, userID string) ([]domain.Membership, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT facility_id, role FROM memberships WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("query memberships: %w", err)
	}
	defer rows.Close()

	var out []domain.Membership
	for rows.Next() {
		var m domain.Membership
		if err := rows.Scan(&m.FacilityID, &m.Role); err != nil {
			return nil, fmt.Errorf("scan membership: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (p *Postgres) InsertRefreshToken(ctx context.Context, t domain.RefreshToken) error {
	var facility *string
	if t.FacilityID != "" {
		facility = &t.FacilityID
	}
	_, err := p.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, family_id, token_hash, facility_id, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		t.UserID, t.FamilyID, t.TokenHash, facility, t.ExpiresAt)
	if err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

func (p *Postgres) RefreshTokenByHash(ctx context.Context, hash string) (domain.RefreshToken, error) {
	var t domain.RefreshToken
	err := p.pool.QueryRow(ctx, `
		SELECT id, user_id, family_id, token_hash, COALESCE(facility_id::text, ''), expires_at, used_at, revoked_at
		FROM refresh_tokens WHERE token_hash = $1`, hash,
	).Scan(&t.ID, &t.UserID, &t.FamilyID, &t.TokenHash, &t.FacilityID, &t.ExpiresAt, &t.UsedAt, &t.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RefreshToken{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.RefreshToken{}, fmt.Errorf("query refresh token: %w", err)
	}
	return t, nil
}

// MarkRefreshTokenUsed is atomic: of two concurrent refreshes with the same token, exactly one wins.
func (p *Postgres) MarkRefreshTokenUsed(ctx context.Context, id string, at time.Time) (bool, error) {
	tag, err := p.pool.Exec(ctx,
		`UPDATE refresh_tokens SET used_at = $2 WHERE id = $1 AND used_at IS NULL`, id, at)
	if err != nil {
		return false, fmt.Errorf("mark refresh token used: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (p *Postgres) RevokeFamily(ctx context.Context, familyID string, at time.Time) error {
	_, err := p.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = $2 WHERE family_id = $1 AND revoked_at IS NULL`, familyID, at)
	if err != nil {
		return fmt.Errorf("revoke token family: %w", err)
	}
	return nil
}
