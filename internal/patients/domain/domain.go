// Package domain holds the patients module's types, errors and storage port. No HTTP or SQL here.
package domain

import (
	"context"
	"errors"
	"time"
)

// Sex values.
const (
	SexFemale = "F"
	SexMale   = "M"
)

// Payment types.
const (
	PaymentBPJS      = "bpjs"
	PaymentPrivate   = "private"
	PaymentInsurance = "insurance"
)

// Roles are the strings carried in the access token. Kept local so this module does not import auth.
const (
	roleAdmin      = "admin"
	roleDoctor     = "doctor"
	roleNurse      = "nurse"
	rolePharmacist = "pharmacist"
	roleFinance    = "finance"
	roleStaff      = "staff"
)

// CanRead reports whether a facility role may view patients.
func CanRead(role string) bool {
	switch role {
	case roleAdmin, roleDoctor, roleNurse, rolePharmacist, roleFinance, roleStaff:
		return true
	}
	return false
}

// CanWrite reports whether a facility role may register and edit patients.
func CanWrite(role string) bool {
	switch role {
	case roleAdmin, roleDoctor, roleNurse, roleStaff:
		return true
	}
	return false
}

// CanArchive reports whether a facility role may remove patients from active lists.
func CanArchive(role string) bool { return role == roleAdmin }

// Patient is a person registered at one facility.
type Patient struct {
	ID         string
	FacilityID string
	MRN        string // medical record number, unique per facility

	FullName   string
	Sex        string
	BirthDate  time.Time // calendar date; time-of-day is zero
	BirthPlace string
	NIK        string // 16 digits, optional

	GuardianName     string
	GuardianRelation string
	Phone            string
	Email            string
	Address          string

	PaymentType string
	BPJSNumber  string

	Allergies  []string
	Conditions []string
	Notes      string

	UserID    string // linked patient app account, if any
	ConsentAt time.Time
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Cursor is a keyset pagination position (newest first).
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// ListFilter selects a page of patients.
type ListFilter struct {
	Query  string  // matches name, MRN or phone
	Cursor *Cursor // nil for the first page
	Limit  int
}

// Page is one page of results.
type Page struct {
	Patients []Patient
	Next     *Cursor // nil when there are no more
}

// Sentinel errors.
var (
	ErrNotFound  = errors.New("patient not found")
	ErrForbidden = errors.New("forbidden")
	ErrNIKTaken  = errors.New("nik already registered at this facility")
)

// ValidationError lists invalid fields (field name -> reason).
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "validation failed" }

// Store is the persistence port. Every method is scoped to a facility: a patient of another
// facility is indistinguishable from a missing one (ErrNotFound).
type Store interface {
	// Create assigns the ID and MRN. Returns ErrNIKTaken on a duplicate active NIK.
	Create(ctx context.Context, facilityID string, p Patient) (Patient, error)
	Get(ctx context.Context, facilityID, id string) (Patient, error)
	// Update replaces the editable fields of p.ID. ErrNotFound, ErrNIKTaken.
	Update(ctx context.Context, facilityID string, p Patient) (Patient, error)
	Archive(ctx context.Context, facilityID, id string, at time.Time) error
	// List returns up to f.Limit+1 rows so the caller can tell whether another page exists.
	List(ctx context.Context, facilityID string, f ListFilter) ([]Patient, error)
}
