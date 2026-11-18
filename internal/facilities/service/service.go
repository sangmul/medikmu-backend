// Package service implements facility, team and invite use cases. Authorization (role + facility) and
// auditing happen here, never only in the HTTP layer.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sangmul/medikmu-backend/internal/facilities/domain"
	"github.com/sangmul/medikmu-backend/internal/platform/audit"
	"github.com/sangmul/medikmu-backend/internal/platform/authctx"
)

// InviteTTL is how long an invite stays valid.
const InviteTTL = 7 * 24 * time.Hour

var (
	uuidRe     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	nonDigits  = regexp.MustCompile(`\D`)
	phoneChars = regexp.MustCompile(`^[+\d\s().-]*$`)
)

// Service holds the facilities use cases.
type Service struct {
	store domain.Store
	audit audit.Recorder
	now   func() time.Time
}

// New builds a Service.
func New(store domain.Store, rec audit.Recorder) *Service {
	return &Service{store: store, audit: rec, now: time.Now}
}

// FacilityInput is the writable part of a facility.
type FacilityInput struct {
	Name    string
	Kind    string // only used on create
	Phone   string
	Address string
}

// CreateFacility makes the caller the admin of a new facility. No active facility is needed:
// this is how a doctor or clinic starts. The caller then calls switch-facility to work inside it.
func (s *Service) CreateFacility(ctx context.Context, who authctx.Principal, in FacilityInput) (domain.Facility, error) {
	if who.UserID == "" {
		return domain.Facility{}, domain.ErrForbidden
	}
	f, err := validateFacility(in, true)
	if err != nil {
		return domain.Facility{}, err
	}
	f.CreatedBy = who.UserID
	created, err := s.store.CreateFacility(ctx, f)
	if err != nil {
		return domain.Facility{}, err
	}
	if err := s.record(ctx, audit.Entry{FacilityID: created.ID, ActorID: who.UserID, Action: "facility.create", Resource: "facility", ResourceID: created.ID}); err != nil {
		return domain.Facility{}, err
	}
	return created, nil
}

// Current returns the caller's active facility. Any staff role.
func (s *Service) Current(ctx context.Context, who authctx.Principal) (domain.Facility, error) {
	if !who.HasFacility() || !domain.ValidRole(who.Role) {
		return domain.Facility{}, domain.ErrForbidden
	}
	return s.store.FacilityByID(ctx, who.FacilityID)
}

// UpdateCurrent edits the active facility's profile. Admin only; the kind cannot change.
func (s *Service) UpdateCurrent(ctx context.Context, who authctx.Principal, in FacilityInput) (domain.Facility, error) {
	if !who.HasFacility() || who.Role != domain.RoleAdmin {
		return domain.Facility{}, domain.ErrForbidden
	}
	f, err := validateFacility(in, false)
	if err != nil {
		return domain.Facility{}, err
	}
	f.ID = who.FacilityID
	updated, err := s.store.UpdateFacility(ctx, f)
	if err != nil {
		return domain.Facility{}, err
	}
	if err := s.record(ctx, audit.Entry{FacilityID: who.FacilityID, ActorID: who.UserID, Action: "facility.update", Resource: "facility", ResourceID: who.FacilityID}); err != nil {
		return domain.Facility{}, err
	}
	return updated, nil
}

// Team lists the active facility's members. Admin only (it exposes staff emails).
func (s *Service) Team(ctx context.Context, who authctx.Principal) ([]domain.Member, error) {
	if !s.isAdmin(who) {
		return nil, domain.ErrForbidden
	}
	return s.store.Members(ctx, who.FacilityID)
}

// RemoveMember removes a staff member. The last admin cannot be removed.
func (s *Service) RemoveMember(ctx context.Context, who authctx.Principal, userID string) error {
	if !s.isAdmin(who) {
		return domain.ErrForbidden
	}
	if !uuidRe.MatchString(userID) {
		return domain.ErrNotFound
	}
	if err := s.store.RemoveMember(ctx, who.FacilityID, userID); err != nil {
		return err
	}
	return s.record(ctx, audit.Entry{FacilityID: who.FacilityID, ActorID: who.UserID, Action: "team.remove", Resource: "user", ResourceID: userID})
}

// InviteResult is a created invite plus its one-time token.
type InviteResult struct {
	Invite domain.Invite
	Token  string // shown once; only its hash is stored
}

// Invite creates an invite for email with role. Admin only. No email is sent: the admin shares the token.
func (s *Service) Invite(ctx context.Context, who authctx.Principal, email, role string) (InviteResult, error) {
	if !s.isAdmin(who) {
		return InviteResult{}, domain.ErrForbidden
	}
	fields := map[string]string{}
	email = strings.ToLower(strings.TrimSpace(email))
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
		fields["email"] = "format email tidak valid"
	}
	if !domain.ValidRole(role) {
		fields["role"] = "pilih admin, doctor, nurse, pharmacist, finance, atau staff"
	}
	if len(fields) > 0 {
		return InviteResult{}, &domain.ValidationError{Fields: fields}
	}

	token, hash, err := newToken()
	if err != nil {
		return InviteResult{}, err
	}
	now := s.now()
	inv, err := s.store.CreateInvite(ctx, domain.Invite{
		FacilityID: who.FacilityID, Email: email, Role: role, TokenHash: hash,
		InvitedBy: who.UserID, ExpiresAt: now.Add(InviteTTL),
	})
	if err != nil {
		return InviteResult{}, err
	}
	if err := s.record(ctx, audit.Entry{FacilityID: who.FacilityID, ActorID: who.UserID, Action: "invite.create", Resource: "invite", ResourceID: inv.ID}); err != nil {
		return InviteResult{}, err
	}
	return InviteResult{Invite: inv, Token: token}, nil
}

// Invites lists pending invites. Admin only.
func (s *Service) Invites(ctx context.Context, who authctx.Principal) ([]domain.Invite, error) {
	if !s.isAdmin(who) {
		return nil, domain.ErrForbidden
	}
	return s.store.PendingInvites(ctx, who.FacilityID, s.now())
}

// RevokeInvite cancels a pending invite. Admin only.
func (s *Service) RevokeInvite(ctx context.Context, who authctx.Principal, id string) error {
	if !s.isAdmin(who) {
		return domain.ErrForbidden
	}
	if !uuidRe.MatchString(id) {
		return domain.ErrNotFound
	}
	if err := s.store.RevokeInvite(ctx, who.FacilityID, id, s.now()); err != nil {
		return err
	}
	return s.record(ctx, audit.Entry{FacilityID: who.FacilityID, ActorID: who.UserID, Action: "invite.revoke", Resource: "invite", ResourceID: id})
}

// AcceptInvite joins the caller to the invite's facility. The caller's account email must match the
// invite email. Any failure to match returns ErrInviteInvalid, so tokens cannot be probed.
func (s *Service) AcceptInvite(ctx context.Context, who authctx.Principal, token string) (domain.Invite, error) {
	if who.UserID == "" {
		return domain.Invite{}, domain.ErrForbidden
	}
	if token == "" {
		return domain.Invite{}, domain.ErrInviteInvalid
	}
	inv, err := s.store.InviteByHash(ctx, hashToken(token))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Invite{}, domain.ErrInviteInvalid
		}
		return domain.Invite{}, err
	}
	email, err := s.store.UserEmail(ctx, who.UserID)
	if err != nil {
		return domain.Invite{}, err
	}
	if !strings.EqualFold(email, inv.Email) {
		return domain.Invite{}, domain.ErrInviteInvalid
	}
	accepted, err := s.store.AcceptInvite(ctx, inv.ID, who.UserID, s.now())
	if err != nil {
		return domain.Invite{}, err
	}
	if err := s.record(ctx, audit.Entry{FacilityID: accepted.FacilityID, ActorID: who.UserID, Action: "invite.accept", Resource: "invite", ResourceID: accepted.ID}); err != nil {
		return domain.Invite{}, err
	}
	return accepted, nil
}

func (s *Service) isAdmin(who authctx.Principal) bool {
	return who.HasFacility() && who.Role == domain.RoleAdmin
}

func (s *Service) record(ctx context.Context, e audit.Entry) error {
	if err := s.audit.Record(ctx, e); err != nil {
		return fmt.Errorf("audit %s: %w", e.Action, err)
	}
	return nil
}

func validateFacility(in FacilityInput, create bool) (domain.Facility, error) {
	fields := map[string]string{}
	f := domain.Facility{Name: strings.TrimSpace(in.Name), Address: strings.TrimSpace(in.Address)}

	if n := utf8.RuneCountInString(f.Name); n < 3 || n > 120 {
		fields["name"] = "nama harus 3-120 karakter"
	}
	if create {
		switch in.Kind {
		case domain.KindSoloDoctor, domain.KindClinic, domain.KindHospital:
			f.Kind = in.Kind
		default:
			fields["kind"] = "pilih solo_doctor, clinic, atau hospital"
		}
	}
	if phone := strings.TrimSpace(in.Phone); phone != "" {
		digits := nonDigits.ReplaceAllString(phone, "")
		if !phoneChars.MatchString(phone) || len(digits) < 6 || len(digits) > 15 {
			fields["phone"] = "nomor telepon tidak valid"
		} else {
			f.Phone = phone
		}
	}
	if utf8.RuneCountInString(f.Address) > 300 {
		fields["address"] = "maksimal 300 karakter"
	}
	if len(fields) > 0 {
		return domain.Facility{}, &domain.ValidationError{Fields: fields}
	}
	return f, nil
}

func newToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate invite token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
