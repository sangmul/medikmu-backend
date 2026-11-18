// Package domain holds the family module: the patient app's own list of people an account manages
// (themselves, children, parents). This data belongs to an account (user_id), not to a facility.
package domain

import (
	"context"
	"errors"
	"time"
)

// MaxMembersPerUser caps the list size (abuse guard; far above any real family).
const MaxMembersPerUser = 20

// Relations.
const (
	RelationSelf    = "self"
	RelationSpouse  = "spouse"
	RelationChild   = "child"
	RelationParent  = "parent"
	RelationSibling = "sibling"
	RelationOther   = "other"
)

// Member is a person managed by a patient app account.
type Member struct {
	ID         string
	UserID     string
	FullName   string
	Relation   string
	Sex        string // F | M
	BirthDate  time.Time
	NIK        string // 16 digits, optional
	AvatarTone string // purple | blue | orange | teal

	// BPJS card (optional). Whether the card is active needs BPJS integration and is not stored.
	BPJSNumber     string // 13 digits
	BPJSHolderName string
	BPJSClass      int // 1..3, 0 when unknown

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Sentinel errors.
var (
	ErrNotFound     = errors.New("family member not found")
	ErrNIKTaken     = errors.New("nik already on this account")
	ErrLimitReached = errors.New("family member limit reached")
)

// ValidationError lists invalid fields (field name -> reason).
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "validation failed" }

// Store is the persistence port. Every method is scoped to the owning account: another account's member is
// indistinguishable from a missing one (ErrNotFound).
type Store interface {
	List(ctx context.Context, userID string) ([]Member, error)
	Get(ctx context.Context, userID, id string) (Member, error)
	Create(ctx context.Context, userID string, m Member) (Member, error) // ErrNIKTaken, ErrLimitReached
	Update(ctx context.Context, userID string, m Member) (Member, error) // ErrNotFound, ErrNIKTaken
	SetNIK(ctx context.Context, userID, id, nik string) (Member, error)  // ErrNotFound, ErrNIKTaken
	Delete(ctx context.Context, userID, id string) error                 // ErrNotFound
}
