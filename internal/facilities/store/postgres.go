// Package store is the PostgreSQL implementation of facilities' domain.Store.
//
// Members and UserEmail read the auth-owned `users` table with a plain join. That is a deliberate shortcut
// to avoid a service round trip per member; if users ever move out of this database, replace the join
// with a call to the auth service.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sangmul/medikmu-backend/internal/facilities/domain"
)

// Postgres implements domain.Store on a pgx pool.
type Postgres struct{ pool *pgxpool.Pool }

var _ domain.Store = (*Postgres)(nil)

// New returns a Postgres-backed store.
func New(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

const (
	pgUniqueViolation = "23505"
	pendingInviteIdx  = "invites_pending_uidx"
)

func isUnique(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && (constraint == "" || pgErr.ConstraintName == constraint)
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

const facilityCols = `id, name, kind, phone, address, COALESCE(created_by::text, ''), created_at, updated_at`

func scanFacility(row pgx.Row) (domain.Facility, error) {
	var f domain.Facility
	err := row.Scan(&f.ID, &f.Name, &f.Kind, &f.Phone, &f.Address, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Facility{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Facility{}, fmt.Errorf("scan facility: %w", err)
	}
	return f, nil
}

// CreateFacility inserts the facility and its first admin in one transaction. A per-user advisory lock
// serializes concurrent creates so the MaxFacilitiesPerUser cap cannot be exceeded by a race.
func (s *Postgres) CreateFacility(ctx context.Context, f domain.Facility) (domain.Facility, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Facility{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "facility-create:"+f.CreatedBy); err != nil {
		return domain.Facility{}, fmt.Errorf("lock: %w", err)
	}
	var owned int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM facilities WHERE created_by = $1`, f.CreatedBy).Scan(&owned); err != nil {
		return domain.Facility{}, fmt.Errorf("count facilities: %w", err)
	}
	if owned >= domain.MaxFacilitiesPerUser {
		return domain.Facility{}, domain.ErrLimitReached
	}

	created, err := scanFacility(tx.QueryRow(ctx,
		`INSERT INTO facilities (name, kind, phone, address, created_by) VALUES ($1, $2, $3, $4, $5) RETURNING `+facilityCols,
		f.Name, f.Kind, f.Phone, f.Address, nullable(f.CreatedBy)))
	if err != nil {
		return domain.Facility{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO memberships (user_id, facility_id, role) VALUES ($1, $2, 'admin')`,
		f.CreatedBy, created.ID); err != nil {
		return domain.Facility{}, fmt.Errorf("add creator as admin: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Facility{}, fmt.Errorf("commit: %w", err)
	}
	return created, nil
}

func (s *Postgres) FacilityByID(ctx context.Context, id string) (domain.Facility, error) {
	return scanFacility(s.pool.QueryRow(ctx, `SELECT `+facilityCols+` FROM facilities WHERE id = $1`, id))
}

func (s *Postgres) UpdateFacility(ctx context.Context, f domain.Facility) (domain.Facility, error) {
	return scanFacility(s.pool.QueryRow(ctx,
		`UPDATE facilities SET name = $2, phone = $3, address = $4, updated_at = now() WHERE id = $1 RETURNING `+facilityCols,
		f.ID, f.Name, f.Phone, f.Address))
}

func (s *Postgres) Members(ctx context.Context, facilityID string) ([]domain.Member, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m.user_id, u.full_name, u.email, m.role, m.created_at
		FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.facility_id = $1
		ORDER BY m.created_at, m.user_id`, facilityID)
	if err != nil {
		return nil, fmt.Errorf("query members: %w", err)
	}
	defer rows.Close()
	var out []domain.Member
	for rows.Next() {
		var m domain.Member
		if err := rows.Scan(&m.UserID, &m.FullName, &m.Email, &m.Role, &m.JoinedAt); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// RemoveMember locks the facility row so two concurrent removals cannot each see "another admin exists".
func (s *Postgres) RemoveMember(ctx context.Context, facilityID, userID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT 1 FROM facilities WHERE id = $1 FOR UPDATE`, facilityID); err != nil {
		return fmt.Errorf("lock facility: %w", err)
	}
	var role string
	err = tx.QueryRow(ctx, `SELECT role FROM memberships WHERE facility_id = $1 AND user_id = $2`, facilityID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("find member: %w", err)
	}
	if role == domain.RoleAdmin {
		var admins int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM memberships WHERE facility_id = $1 AND role = 'admin'`, facilityID).Scan(&admins); err != nil {
			return fmt.Errorf("count admins: %w", err)
		}
		if admins <= 1 {
			return domain.ErrLastAdmin
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM memberships WHERE facility_id = $1 AND user_id = $2`, facilityID, userID); err != nil {
		return fmt.Errorf("delete member: %w", err)
	}
	return tx.Commit(ctx)
}

const inviteCols = `id, facility_id, email, role, token_hash, COALESCE(invited_by::text, ''), expires_at, created_at`

func scanInvite(row pgx.Row) (domain.Invite, error) {
	var i domain.Invite
	err := row.Scan(&i.ID, &i.FacilityID, &i.Email, &i.Role, &i.TokenHash, &i.InvitedBy, &i.ExpiresAt, &i.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invite{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Invite{}, fmt.Errorf("scan invite: %w", err)
	}
	return i, nil
}

func (s *Postgres) CreateInvite(ctx context.Context, inv domain.Invite) (domain.Invite, error) {
	var member bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM memberships m JOIN users u ON u.id = m.user_id
		               WHERE m.facility_id = $1 AND lower(u.email) = $2)`,
		inv.FacilityID, inv.Email).Scan(&member); err != nil {
		return domain.Invite{}, fmt.Errorf("check membership: %w", err)
	}
	if member {
		return domain.Invite{}, domain.ErrAlreadyMember
	}
	created, err := scanInvite(s.pool.QueryRow(ctx, `
		INSERT INTO invites (facility_id, email, role, token_hash, invited_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+inviteCols,
		inv.FacilityID, inv.Email, inv.Role, inv.TokenHash, nullable(inv.InvitedBy), inv.ExpiresAt))
	if isUnique(err, pendingInviteIdx) {
		return domain.Invite{}, domain.ErrInvitePending
	}
	return created, err
}

func (s *Postgres) PendingInvites(ctx context.Context, facilityID string, now time.Time) ([]domain.Invite, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+inviteCols+` FROM invites
		WHERE facility_id = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > $2
		ORDER BY created_at DESC, id DESC`, facilityID, now)
	if err != nil {
		return nil, fmt.Errorf("query invites: %w", err)
	}
	defer rows.Close()
	var out []domain.Invite
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (s *Postgres) RevokeInvite(ctx context.Context, facilityID, id string, at time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE invites SET revoked_at = $3
		WHERE id = $2 AND facility_id = $1 AND accepted_at IS NULL AND revoked_at IS NULL`, facilityID, id, at)
	if err != nil {
		return fmt.Errorf("revoke invite: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Postgres) InviteByHash(ctx context.Context, hash string) (domain.Invite, error) {
	return scanInvite(s.pool.QueryRow(ctx, `SELECT `+inviteCols+` FROM invites WHERE token_hash = $1`, hash))
}

// AcceptInvite consumes the invite and adds the membership in one transaction: if the user is already a
// member, the whole thing rolls back and the invite stays pending.
func (s *Postgres) AcceptInvite(ctx context.Context, inviteID, userID string, at time.Time) (domain.Invite, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Invite{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	inv, err := scanInvite(tx.QueryRow(ctx, `
		UPDATE invites SET accepted_at = $2
		WHERE id = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > $2
		RETURNING `+inviteCols, inviteID, at))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Invite{}, domain.ErrInviteInvalid
	}
	if err != nil {
		return domain.Invite{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO memberships (user_id, facility_id, role) VALUES ($1, $2, $3)`,
		userID, inv.FacilityID, inv.Role); err != nil {
		if isUnique(err, "") {
			return domain.Invite{}, domain.ErrAlreadyMember
		}
		return domain.Invite{}, fmt.Errorf("add membership: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Invite{}, fmt.Errorf("commit: %w", err)
	}
	return inv, nil
}

func (s *Postgres) UserEmail(ctx context.Context, userID string) (string, error) {
	var email string
	err := s.pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("query user email: %w", err)
	}
	return email, nil
}
