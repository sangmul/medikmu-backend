// Package service implements the auth use cases: register, login, refresh, logout, switch facility.
package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sangmul/medikmu-backend/internal/auth/domain"
)

// Config configures token issuing. JWTSecret must be at least 32 bytes.
type Config struct {
	JWTSecret  []byte
	Issuer     string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

// Service holds the auth use cases.
type Service struct {
	store domain.Store
	cfg   Config
	now   func() time.Time
	// dummyHash is verified against when the user does not exist, so login timing
	// does not reveal whether an email is registered.
	dummyHash string
}

// New builds a Service. It returns an error for an unusable configuration.
func New(store domain.Store, cfg Config) (*Service, error) {
	if len(cfg.JWTSecret) < 32 {
		return nil, errors.New("auth: JWT secret must be at least 32 bytes")
	}
	if cfg.AccessTTL <= 0 || cfg.RefreshTTL <= 0 {
		return nil, errors.New("auth: token TTLs must be positive")
	}
	if cfg.Issuer == "" {
		cfg.Issuer = "medikmu"
	}
	dummy, err := hashPassword(rand.Text())
	if err != nil {
		return nil, err
	}
	return &Service{store: store, cfg: cfg, now: time.Now, dummyHash: dummy}, nil
}

// RegisterInput is the data needed to create an account.
type RegisterInput struct {
	Email    string
	Phone    string // optional
	Password string
	FullName string
}

// Register creates a patient account and signs the user in.
func (s *Service) Register(ctx context.Context, in RegisterInput) (domain.TokenPair, error) {
	email := normalizeEmail(in.Email)
	fields := map[string]string{}
	if _, err := mail.ParseAddress(email); err != nil || strings.ContainsAny(email, " <>") {
		fields["email"] = "format email tidak valid"
	}
	if n := utf8.RuneCountInString(in.Password); n < 8 || n > 128 {
		fields["password"] = "kata sandi harus 8-128 karakter"
	}
	name := strings.TrimSpace(in.FullName)
	if name == "" || utf8.RuneCountInString(name) > 120 {
		fields["full_name"] = "nama wajib diisi (maksimal 120 karakter)"
	}
	phone, msg := normalizePhone(in.Phone)
	if msg != "" {
		fields["phone"] = msg
	}
	if len(fields) > 0 {
		return domain.TokenPair{}, &domain.ValidationError{Fields: fields}
	}

	hash, err := hashPassword(in.Password)
	if err != nil {
		return domain.TokenPair{}, err
	}
	user, err := s.store.CreateUser(ctx, domain.User{
		Email:        email,
		Phone:        phone,
		PasswordHash: hash,
		FullName:     name,
	})
	if err != nil {
		return domain.TokenPair{}, err
	}
	return s.startSession(ctx, user.ID, "")
}

// Login verifies credentials and starts a session. If the user belongs to exactly one
// facility it becomes the active facility; otherwise use SwitchFacility.
func (s *Service) Login(ctx context.Context, email, password string) (domain.TokenPair, error) {
	user, err := s.store.UserByEmail(ctx, normalizeEmail(email))
	if errors.Is(err, domain.ErrNotFound) {
		_, _ = verifyPassword(password, s.dummyHash) // equalize timing
		return domain.TokenPair{}, domain.ErrInvalidCredentials
	}
	if err != nil {
		return domain.TokenPair{}, err
	}
	ok, err := verifyPassword(password, user.PasswordHash)
	if err != nil || !ok {
		return domain.TokenPair{}, domain.ErrInvalidCredentials
	}

	memberships, err := s.store.Memberships(ctx, user.ID)
	if err != nil {
		return domain.TokenPair{}, err
	}
	facilityID := ""
	if len(memberships) == 1 {
		facilityID = memberships[0].FacilityID
	}
	return s.startSession(ctx, user.ID, facilityID)
}

// Refresh rotates a refresh token. Presenting an already-used token is treated as theft:
// the whole session family is revoked.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (domain.TokenPair, error) {
	now := s.now()
	old, err := s.consume(ctx, refreshToken, now)
	if err != nil {
		return domain.TokenPair{}, err
	}
	return s.issue(ctx, old.UserID, old.FamilyID, old.FacilityID, now)
}

// Logout revokes the session the refresh token belongs to. Unknown tokens are ignored.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	t, err := s.store.RefreshTokenByHash(ctx, hashToken(refreshToken))
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.store.RevokeFamily(ctx, t.FamilyID, s.now())
}

// SwitchFacility moves the session to another facility the user belongs to and returns new tokens.
// An empty facilityID switches to the personal (patient) context.
func (s *Service) SwitchFacility(ctx context.Context, userID, refreshToken, facilityID string) (domain.TokenPair, error) {
	// Check membership before consuming the refresh token, so a rejected switch
	// does not burn the caller's session.
	if facilityID != "" {
		if _, ok, err := s.roleIn(ctx, userID, facilityID); err != nil {
			return domain.TokenPair{}, err
		} else if !ok {
			return domain.TokenPair{}, domain.ErrForbidden
		}
	}
	now := s.now()
	old, err := s.consume(ctx, refreshToken, now)
	if err != nil {
		return domain.TokenPair{}, err
	}
	if old.UserID != userID {
		_ = s.store.RevokeFamily(ctx, old.FamilyID, now)
		return domain.TokenPair{}, domain.ErrInvalidToken
	}
	return s.issue(ctx, userID, old.FamilyID, facilityID, now)
}

// Me returns the current user and memberships.
func (s *Service) Me(ctx context.Context, userID string) (domain.Me, error) {
	user, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return domain.Me{}, err
	}
	memberships, err := s.store.Memberships(ctx, userID)
	if err != nil {
		return domain.Me{}, err
	}
	return domain.Me{User: user, Memberships: memberships}, nil
}

// UpdateProfile changes the caller's display name and phone. An empty phone clears it; changing the phone
// resets its verification.
func (s *Service) UpdateProfile(ctx context.Context, userID, fullName, phone string) (domain.User, error) {
	fields := map[string]string{}
	name := strings.TrimSpace(fullName)
	if name == "" || utf8.RuneCountInString(name) > 120 {
		fields["full_name"] = "nama wajib diisi (maksimal 120 karakter)"
	}
	phone, msg := normalizePhone(phone)
	if msg != "" {
		fields["phone"] = msg
	}
	if len(fields) > 0 {
		return domain.User{}, &domain.ValidationError{Fields: fields}
	}
	return s.store.UpdateProfile(ctx, userID, name, phone)
}

// consume validates a refresh token and marks it used. On reuse it revokes the family.
func (s *Service) consume(ctx context.Context, token string, now time.Time) (domain.RefreshToken, error) {
	if token == "" {
		return domain.RefreshToken{}, domain.ErrInvalidToken
	}
	t, err := s.store.RefreshTokenByHash(ctx, hashToken(token))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.RefreshToken{}, domain.ErrInvalidToken
	}
	if err != nil {
		return domain.RefreshToken{}, err
	}
	if t.RevokedAt != nil || !now.Before(t.ExpiresAt) {
		return domain.RefreshToken{}, domain.ErrInvalidToken
	}
	won, err := s.store.MarkRefreshTokenUsed(ctx, t.ID, now)
	if err != nil {
		return domain.RefreshToken{}, err
	}
	if !won || t.UsedAt != nil {
		if err := s.store.RevokeFamily(ctx, t.FamilyID, now); err != nil {
			return domain.RefreshToken{}, err
		}
		return domain.RefreshToken{}, domain.ErrInvalidToken
	}
	return t, nil
}

func (s *Service) startSession(ctx context.Context, userID, facilityID string) (domain.TokenPair, error) {
	return s.issue(ctx, userID, newID(), facilityID, s.now())
}

// issue creates an access token and a new refresh token in the given family.
func (s *Service) issue(ctx context.Context, userID, familyID, facilityID string, now time.Time) (domain.TokenPair, error) {
	role := ""
	if facilityID != "" {
		r, ok, err := s.roleIn(ctx, userID, facilityID)
		if err != nil {
			return domain.TokenPair{}, err
		}
		if !ok {
			return domain.TokenPair{}, domain.ErrForbidden
		}
		role = r
	}

	access, err := s.signAccess(userID, facilityID, role, now)
	if err != nil {
		return domain.TokenPair{}, fmt.Errorf("sign access token: %w", err)
	}
	refresh, hash, err := newRefreshToken()
	if err != nil {
		return domain.TokenPair{}, err
	}
	if err := s.store.InsertRefreshToken(ctx, domain.RefreshToken{
		UserID:     userID,
		FamilyID:   familyID,
		TokenHash:  hash,
		FacilityID: facilityID,
		ExpiresAt:  now.Add(s.cfg.RefreshTTL),
	}); err != nil {
		return domain.TokenPair{}, err
	}
	return domain.TokenPair{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int(s.cfg.AccessTTL.Seconds()),
	}, nil
}

func (s *Service) roleIn(ctx context.Context, userID, facilityID string) (string, bool, error) {
	memberships, err := s.store.Memberships(ctx, userID)
	if err != nil {
		return "", false, err
	}
	for _, m := range memberships {
		if m.FacilityID == facilityID {
			return m.Role, true, nil
		}
	}
	return "", false, nil
}

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// normalizePhone keeps digits (and a leading +) so "0812-3456-7890" and "081234567890" are the same number.
// An empty phone is valid (it is optional). The message is non-empty when the number is unusable.
func normalizePhone(phone string) (normalized, msg string) {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return "", ""
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	if len(digits) < 9 || len(digits) > 15 {
		return "", "nomor telepon harus 9-15 digit"
	}
	if strings.HasPrefix(phone, "+") {
		return "+" + digits, ""
	}
	return digits, ""
}
