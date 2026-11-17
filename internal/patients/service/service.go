// Package service implements the patients use cases. Authorization (role + facility) and auditing
// happen here, never only in the HTTP layer.
package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/sangmul/medikmu-backend/internal/patients/domain"
	"github.com/sangmul/medikmu-backend/internal/platform/audit"
	"github.com/sangmul/medikmu-backend/internal/platform/authctx"
)

const (
	defaultLimit = 20
	maxLimit     = 100
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Service holds the patients use cases.
type Service struct {
	store domain.Store
	audit audit.Recorder
	now   func() time.Time
}

// New builds a Service.
func New(store domain.Store, rec audit.Recorder) *Service {
	return &Service{store: store, audit: rec, now: time.Now}
}

// CreateInput is Input plus the guardian/patient consent flag required to register someone.
type CreateInput struct {
	Input
	Consent bool
}

// Create registers a patient at the caller's facility.
func (s *Service) Create(ctx context.Context, who authctx.Principal, in CreateInput) (domain.Patient, error) {
	if !who.HasFacility() || !domain.CanWrite(who.Role) {
		return domain.Patient{}, domain.ErrForbidden
	}
	p, err := build(in.Input, s.now())
	if err != nil {
		return domain.Patient{}, err
	}
	if !in.Consent {
		return domain.Patient{}, &domain.ValidationError{Fields: map[string]string{
			"consent": "persetujuan pasien/wali wajib untuk menyimpan data",
		}}
	}
	now := s.now()
	p.ConsentAt = now
	p.CreatedBy = who.UserID

	created, err := s.store.Create(ctx, who.FacilityID, p)
	if err != nil {
		return domain.Patient{}, err
	}
	if err := s.record(ctx, who, "patient.create", created.ID); err != nil {
		return domain.Patient{}, err
	}
	return created, nil
}

// Get returns one patient of the caller's facility and records the access.
func (s *Service) Get(ctx context.Context, who authctx.Principal, id string) (domain.Patient, error) {
	if !who.HasFacility() || !domain.CanRead(who.Role) {
		return domain.Patient{}, domain.ErrForbidden
	}
	if !uuidRe.MatchString(id) {
		return domain.Patient{}, domain.ErrNotFound
	}
	p, err := s.store.Get(ctx, who.FacilityID, id)
	if err != nil {
		return domain.Patient{}, err
	}
	if err := s.record(ctx, who, "patient.read", p.ID); err != nil {
		return domain.Patient{}, err
	}
	return p, nil
}

// Update replaces the editable fields of a patient.
func (s *Service) Update(ctx context.Context, who authctx.Principal, id string, in Input) (domain.Patient, error) {
	if !who.HasFacility() || !domain.CanWrite(who.Role) {
		return domain.Patient{}, domain.ErrForbidden
	}
	if !uuidRe.MatchString(id) {
		return domain.Patient{}, domain.ErrNotFound
	}
	p, err := build(in, s.now())
	if err != nil {
		return domain.Patient{}, err
	}
	p.ID = id
	updated, err := s.store.Update(ctx, who.FacilityID, p)
	if err != nil {
		return domain.Patient{}, err
	}
	if err := s.record(ctx, who, "patient.update", updated.ID); err != nil {
		return domain.Patient{}, err
	}
	return updated, nil
}

// Archive removes a patient from active lists. The row and its history are kept.
func (s *Service) Archive(ctx context.Context, who authctx.Principal, id string) error {
	if !who.HasFacility() || !domain.CanArchive(who.Role) {
		return domain.ErrForbidden
	}
	if !uuidRe.MatchString(id) {
		return domain.ErrNotFound
	}
	if err := s.store.Archive(ctx, who.FacilityID, id, s.now()); err != nil {
		return err
	}
	return s.record(ctx, who, "patient.archive", id)
}

// List returns a page of the caller's facility patients, newest first.
func (s *Service) List(ctx context.Context, who authctx.Principal, q string, cursor *domain.Cursor, limit int) (domain.Page, error) {
	if !who.HasFacility() || !domain.CanRead(who.Role) {
		return domain.Page{}, domain.ErrForbidden
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)

	rows, err := s.store.List(ctx, who.FacilityID, domain.ListFilter{
		Query: strings.TrimSpace(q), Cursor: cursor, Limit: limit,
	})
	if err != nil {
		return domain.Page{}, err
	}
	page := domain.Page{Patients: rows}
	if len(rows) > limit {
		page.Patients = rows[:limit]
		last := rows[limit-1]
		page.Next = &domain.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	// The search text itself is not logged: names and phone numbers are PII.
	if err := s.record(ctx, who, "patient.list", ""); err != nil {
		return domain.Page{}, err
	}
	return page, nil
}

// record writes an audit entry. Callers return the error: no audit, no data.
func (s *Service) record(ctx context.Context, who authctx.Principal, action, id string) error {
	if err := s.audit.Record(ctx, audit.Entry{
		FacilityID: who.FacilityID, ActorID: who.UserID,
		Action: action, Resource: "patient", ResourceID: id,
	}); err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}
