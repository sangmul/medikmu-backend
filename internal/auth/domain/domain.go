// Package domain holds the auth module's types, errors and the storage interface.
// It has no HTTP or SQL dependencies.
package domain

import (
	"context"
	"errors"
	"time"
)

// Roles a user can hold inside a facility.
const (
	RoleAdmin      = "admin"
	RoleDoctor     = "doctor"
	RoleNurse      = "nurse"
	RolePharmacist = "pharmacist"
	RoleFinance    = "finance"
	RoleStaff      = "staff"
)

// User is an account. A user is a patient by default and may also be staff of facilities.
type User struct {
	ID           string
	Email        string
	Phone        string // optional
	PasswordHash string
	FullName     string
	CreatedAt    time.Time
}

// Membership links a user to a facility with a role.
type Membership struct {
	FacilityID string
	Role       string
}

// RefreshToken is the stored (hashed) form of a refresh token.
type RefreshToken struct {
	ID         string
	UserID     string
	FamilyID   string
	TokenHash  string
	FacilityID string // empty when no facility is active
	ExpiresAt  time.Time
	UsedAt     *time.Time
	RevokedAt  *time.Time
}

// TokenPair is what clients receive after authenticating.
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int // access token lifetime in seconds
}

// Me is the current user with their memberships.
type Me struct {
	User        User
	Memberships []Membership
}

// Sentinel errors. Handlers map these to HTTP statuses; messages never reveal which check failed.
var (
	ErrNotFound           = errors.New("not found")
	ErrEmailTaken         = errors.New("email already registered")
	ErrPhoneTaken         = errors.New("phone already registered")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid token")
	ErrForbidden          = errors.New("forbidden")
)

// ValidationError lists invalid fields (field name -> reason).
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string { return "validation failed" }

// Store is the persistence port implemented by internal/auth/store.
type Store interface {
	CreateUser(ctx context.Context, u User) (User, error) // ErrEmailTaken, ErrPhoneTaken
	UserByEmail(ctx context.Context, email string) (User, error)
	UserByID(ctx context.Context, id string) (User, error)
	Memberships(ctx context.Context, userID string) ([]Membership, error)

	InsertRefreshToken(ctx context.Context, t RefreshToken) error
	RefreshTokenByHash(ctx context.Context, hash string) (RefreshToken, error)
	// MarkRefreshTokenUsed atomically sets used_at if it was unset and reports whether it did.
	MarkRefreshTokenUsed(ctx context.Context, id string, at time.Time) (bool, error)
	RevokeFamily(ctx context.Context, familyID string, at time.Time) error
}
