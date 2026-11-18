// Package domain holds the facilities module's types, errors and storage port: facilities (tenants),
// their team (memberships) and staff invites. No HTTP or SQL here.
package domain

import (
	"context"
	"errors"
	"time"
)

// Facility kinds.
const (
	KindSoloDoctor = "solo_doctor"
	KindClinic     = "clinic"
	KindHospital   = "hospital"
)

// Roles are the strings carried in the access token. Kept local so this module does not import auth.
const (
	RoleAdmin      = "admin"
	RoleDoctor     = "doctor"
	RoleNurse      = "nurse"
	RolePharmacist = "pharmacist"
	RoleFinance    = "finance"
	RoleStaff      = "staff"
)

// ValidRole reports whether r is a known facility role.
func ValidRole(r string) bool {
	switch r {
	case RoleAdmin, RoleDoctor, RoleNurse, RolePharmacist, RoleFinance, RoleStaff:
		return true
	}
	return false
}

// MaxFacilitiesPerUser caps how many facilities one account may create (abuse guard).
const MaxFacilitiesPerUser = 5

// Facility is a tenant: a solo doctor, clinic or hospital.
type Facility struct {
	ID        string
	Name      string
	Kind      string
	Phone     string
	Address   string
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Member is a staff member of a facility.
type Member struct {
	UserID   string
	FullName string
	Email    string
	Role     string
	JoinedAt time.Time
}

// Invite lets a person join a facility with a role. The token itself is never stored, only its hash.
type Invite struct {
	ID         string
	FacilityID string
	Email      string
	Role       string
	TokenHash  string
	InvitedBy  string
	ExpiresAt  time.Time
	CreatedAt  time.Time
}

// Sentinel errors.
var (
	ErrNotFound      = errors.New("not found")
	ErrForbidden     = errors.New("forbidden")
	ErrLimitReached  = errors.New("facility limit reached")
	ErrLastAdmin     = errors.New("cannot remove the last admin")
	ErrAlreadyMember = errors.New("already a member")
	ErrInvitePending = errors.New("an invite is already pending for this email")
	// ErrInviteInvalid is deliberately vague (unknown, used, revoked, expired, or for another email)
	// so a token cannot be probed.
	ErrInviteInvalid = errors.New("invite invalid")
)

// ValidationError lists invalid fields (field name -> reason).
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "validation failed" }

// Store is the persistence port.
type Store interface {
	// CreateFacility inserts f and makes f.CreatedBy its admin, atomically. ErrLimitReached when the
	// creator already owns MaxFacilitiesPerUser facilities.
	CreateFacility(ctx context.Context, f Facility) (Facility, error)
	FacilityByID(ctx context.Context, id string) (Facility, error)
	UpdateFacility(ctx context.Context, f Facility) (Facility, error)

	Members(ctx context.Context, facilityID string) ([]Member, error)
	// RemoveMember deletes the membership. ErrNotFound, or ErrLastAdmin if it would leave no admin.
	RemoveMember(ctx context.Context, facilityID, userID string) error

	// CreateInvite stores a new pending invite. ErrAlreadyMember (that email is already on the team)
	// or ErrInvitePending (one pending invite per email).
	CreateInvite(ctx context.Context, inv Invite) (Invite, error)
	// PendingInvites returns invites that are not accepted, revoked or expired at now.
	PendingInvites(ctx context.Context, facilityID string, now time.Time) ([]Invite, error)
	RevokeInvite(ctx context.Context, facilityID, id string, at time.Time) error
	InviteByHash(ctx context.Context, tokenHash string) (Invite, error)
	// AcceptInvite atomically consumes the invite (ErrInviteInvalid if it is no longer pending at `at`)
	// and adds the membership (ErrAlreadyMember).
	AcceptInvite(ctx context.Context, inviteID, userID string, at time.Time) (Invite, error)

	UserEmail(ctx context.Context, userID string) (string, error)
}
