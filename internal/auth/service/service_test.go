package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/sangmul/medikmu-backend/internal/auth/authtest"
	"github.com/sangmul/medikmu-backend/internal/auth/domain"
	"github.com/sangmul/medikmu-backend/internal/auth/service"
)

var secret = []byte("test-secret-test-secret-test-secret-123")

func newSvc(t *testing.T) (*service.Service, *authtest.MemStore) {
	t.Helper()
	st := authtest.NewMemStore()
	svc, err := service.New(st, service.Config{
		JWTSecret: secret, Issuer: "medikmu-test",
		AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, st
}

func register(t *testing.T, svc *service.Service, email string) domain.TokenPair {
	t.Helper()
	pair, err := svc.Register(context.Background(), service.RegisterInput{
		Email: email, Password: "correct-horse", FullName: "Budi Santoso",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return pair
}

func TestNew_RejectsShortSecret(t *testing.T) {
	_, err := service.New(authtest.NewMemStore(), service.Config{
		JWTSecret: []byte("short"), AccessTTL: time.Minute, RefreshTTL: time.Hour,
	})
	if err == nil {
		t.Fatal("expected error for short secret")
	}
}

func TestRegister(t *testing.T) {
	tests := []struct {
		name    string
		in      service.RegisterInput
		wantErr string // field name expected in ValidationError, "" for success
	}{
		{"ok", service.RegisterInput{Email: "Budi@Example.com ", Password: "correct-horse", FullName: "Budi"}, ""},
		{"bad email", service.RegisterInput{Email: "not-an-email", Password: "correct-horse", FullName: "Budi"}, "email"},
		{"short password", service.RegisterInput{Email: "a@b.co", Password: "short", FullName: "Budi"}, "password"},
		{"missing name", service.RegisterInput{Email: "a@b.co", Password: "correct-horse", FullName: "  "}, "full_name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newSvc(t)
			pair, err := svc.Register(context.Background(), tt.in)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if pair.AccessToken == "" || pair.RefreshToken == "" || pair.ExpiresIn != 900 {
					t.Fatalf("bad token pair: %+v", pair)
				}
				return
			}
			var ve *domain.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want ValidationError, got %v", err)
			}
			if _, ok := ve.Fields[tt.wantErr]; !ok {
				t.Fatalf("want field %q in %v", tt.wantErr, ve.Fields)
			}
		})
	}
}

func TestRegister_DuplicateEmailIsCaseInsensitive(t *testing.T) {
	svc, _ := newSvc(t)
	register(t, svc, "budi@example.com")
	_, err := svc.Register(context.Background(), service.RegisterInput{
		Email: "BUDI@example.com", Password: "correct-horse", FullName: "Lain",
	})
	if !errors.Is(err, domain.ErrEmailTaken) {
		t.Fatalf("want ErrEmailTaken, got %v", err)
	}
}

func TestLogin(t *testing.T) {
	svc, _ := newSvc(t)
	register(t, svc, "budi@example.com")
	ctx := context.Background()

	if _, err := svc.Login(ctx, " Budi@Example.com", "correct-horse"); err != nil {
		t.Fatalf("valid login failed: %v", err)
	}
	// Wrong password and unknown user must be indistinguishable.
	_, errWrong := svc.Login(ctx, "budi@example.com", "wrong-password")
	_, errUnknown := svc.Login(ctx, "ghost@example.com", "correct-horse")
	if !errors.Is(errWrong, domain.ErrInvalidCredentials) || !errors.Is(errUnknown, domain.ErrInvalidCredentials) {
		t.Fatalf("want ErrInvalidCredentials for both, got %v / %v", errWrong, errUnknown)
	}
}

func TestLogin_SingleMembershipActivatesFacility(t *testing.T) {
	svc, st := newSvc(t)
	pair := register(t, svc, "dokter@example.com")
	p, err := svc.VerifyAccessToken(pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	st.AddMembership(p.UserID, "fac-1", domain.RoleDoctor)

	pair, err = svc.Login(context.Background(), "dokter@example.com", "correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := svc.VerifyAccessToken(pair.AccessToken)
	if got.FacilityID != "fac-1" || got.Role != domain.RoleDoctor {
		t.Fatalf("want facility fac-1/doctor, got %+v", got)
	}
}

func TestLogin_MultipleMembershipsStartWithoutFacility(t *testing.T) {
	svc, st := newSvc(t)
	pair := register(t, svc, "dokter@example.com")
	p, _ := svc.VerifyAccessToken(pair.AccessToken)
	st.AddMembership(p.UserID, "fac-1", domain.RoleDoctor)
	st.AddMembership(p.UserID, "fac-2", domain.RoleAdmin)

	pair, err := svc.Login(context.Background(), "dokter@example.com", "correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := svc.VerifyAccessToken(pair.AccessToken)
	if got.HasFacility() {
		t.Fatalf("want no active facility, got %+v", got)
	}
}

func TestRefresh_RotatesAndRejectsReuse(t *testing.T) {
	svc, st := newSvc(t)
	first := register(t, svc, "budi@example.com")
	ctx := context.Background()

	second, err := svc.Refresh(ctx, first.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if second.RefreshToken == first.RefreshToken {
		t.Fatal("refresh token must rotate")
	}

	// Replaying the old token is theft: it fails AND revokes the whole family.
	if _, err := svc.Refresh(ctx, first.RefreshToken); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("reuse: want ErrInvalidToken, got %v", err)
	}
	if st.RevokedCount() != st.TokenCount() {
		t.Fatalf("family not fully revoked: %d/%d", st.RevokedCount(), st.TokenCount())
	}
	if _, err := svc.Refresh(ctx, second.RefreshToken); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("legit token after reuse should be revoked, got %v", err)
	}
}

func TestRefresh_InvalidInputs(t *testing.T) {
	svc, _ := newSvc(t)
	for _, tok := range []string{"", "garbage", strings.Repeat("a", 43)} {
		if _, err := svc.Refresh(context.Background(), tok); !errors.Is(err, domain.ErrInvalidToken) {
			t.Errorf("token %q: want ErrInvalidToken, got %v", tok, err)
		}
	}
}

func TestRefresh_Expired(t *testing.T) {
	st := authtest.NewMemStore()
	svc, _ := service.New(st, service.Config{
		JWTSecret: secret, AccessTTL: time.Minute, RefreshTTL: time.Nanosecond,
	})
	pair, err := svc.Register(context.Background(), service.RegisterInput{
		Email: "a@b.co", Password: "correct-horse", FullName: "A",
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := svc.Refresh(context.Background(), pair.RefreshToken); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("want ErrInvalidToken for expired token, got %v", err)
	}
}

func TestLogout_RevokesSession(t *testing.T) {
	svc, _ := newSvc(t)
	pair := register(t, svc, "budi@example.com")
	ctx := context.Background()

	if err := svc.Logout(ctx, pair.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Refresh(ctx, pair.RefreshToken); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("refresh after logout: want ErrInvalidToken, got %v", err)
	}
	if err := svc.Logout(ctx, "unknown-token"); err != nil {
		t.Fatalf("logout with unknown token must be a no-op, got %v", err)
	}
}

func TestSwitchFacility(t *testing.T) {
	svc, st := newSvc(t)
	pair := register(t, svc, "dokter@example.com")
	p, _ := svc.VerifyAccessToken(pair.AccessToken)
	st.AddMembership(p.UserID, "fac-1", domain.RoleDoctor)
	ctx := context.Background()

	// Not a member: forbidden, and the refresh token must NOT be consumed.
	if _, err := svc.SwitchFacility(ctx, p.UserID, pair.RefreshToken, "fac-other"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}

	next, err := svc.SwitchFacility(ctx, p.UserID, pair.RefreshToken, "fac-1")
	if err != nil {
		t.Fatalf("switch after rejected attempt should still work: %v", err)
	}
	got, _ := svc.VerifyAccessToken(next.AccessToken)
	if got.FacilityID != "fac-1" || got.Role != domain.RoleDoctor {
		t.Fatalf("want fac-1/doctor, got %+v", got)
	}

	// Another user's token cannot be used.
	other := register(t, svc, "lain@example.com")
	if _, err := svc.SwitchFacility(ctx, p.UserID, other.RefreshToken, "fac-1"); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("want ErrInvalidToken for foreign refresh token, got %v", err)
	}
}

// A staff member removed from a facility keeps a valid access token until it expires (at most AccessTTL),
// but can no longer renew it: refreshing re-checks the membership. See docs/security-compliance.md.
func TestRefresh_AfterMembershipRemovedIsRefused(t *testing.T) {
	svc, st := newSvc(t)
	pair := register(t, svc, "dokter@example.com")
	p, _ := svc.VerifyAccessToken(pair.AccessToken)
	st.AddMembership(p.UserID, "fac-1", domain.RoleDoctor)
	ctx := context.Background()

	scoped, err := svc.SwitchFacility(ctx, p.UserID, pair.RefreshToken, "fac-1")
	if err != nil {
		t.Fatal(err)
	}
	st.RemoveMembership(p.UserID, "fac-1")

	if _, err := svc.VerifyAccessToken(scoped.AccessToken); err != nil {
		t.Fatalf("existing access token stays valid until it expires (documented limit): %v", err)
	}
	if _, err := svc.Refresh(ctx, scoped.RefreshToken); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("refresh after removal: want ErrForbidden, got %v", err)
	}
}

func TestUpdateProfile(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	a := register(t, svc, "a@example.com")
	b := register(t, svc, "b@example.com")
	pa, _ := svc.VerifyAccessToken(a.AccessToken)
	pb, _ := svc.VerifyAccessToken(b.AccessToken)

	u, err := svc.UpdateProfile(ctx, pa.UserID, "  Budi Santoso ", "0812-3456-7890")
	if err != nil || u.FullName != "Budi Santoso" || u.Phone != "081234567890" || u.PhoneVerified {
		t.Fatalf("update: %v %+v", err, u)
	}
	if u, _ := svc.UpdateProfile(ctx, pa.UserID, "Budi", "+62 812 3456 7890"); u.Phone != "+6281234567890" {
		t.Fatalf("a leading + must be kept: %q", u.Phone)
	}
	if u, err := svc.UpdateProfile(ctx, pa.UserID, "Budi", ""); err != nil || u.Phone != "" {
		t.Fatalf("empty phone clears it: %v %+v", err, u)
	}

	// Another account cannot take a phone number that is already in use.
	if _, err := svc.UpdateProfile(ctx, pa.UserID, "Budi", "081234567890"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateProfile(ctx, pb.UserID, "Siti", "081234567890"); !errors.Is(err, domain.ErrPhoneTaken) {
		t.Fatalf("want ErrPhoneTaken, got %v", err)
	}

	for name, in := range map[string][2]string{"blank name": {"  ", ""}, "short phone": {"Budi", "123"}, "long phone": {"Budi", "1234567890123456"}} {
		var ve *domain.ValidationError
		if _, err := svc.UpdateProfile(ctx, pa.UserID, in[0], in[1]); !errors.As(err, &ve) {
			t.Errorf("%s: want ValidationError, got %v", name, err)
		}
	}
}

func TestVerifyAccessToken_Rejects(t *testing.T) {
	svc, _ := newSvc(t)
	pair := register(t, svc, "budi@example.com")

	// Tampered signature.
	if _, err := svc.VerifyAccessToken(pair.AccessToken + "x"); !errors.Is(err, domain.ErrInvalidToken) {
		t.Errorf("tampered: got %v", err)
	}
	// Signed with another secret.
	forged := mustSign(t, jwt.SigningMethodHS256, []byte("another-secret-another-secret-12345"), "medikmu-test", time.Hour)
	if _, err := svc.VerifyAccessToken(forged); !errors.Is(err, domain.ErrInvalidToken) {
		t.Errorf("wrong secret: got %v", err)
	}
	// Wrong issuer.
	other := mustSign(t, jwt.SigningMethodHS256, secret, "someone-else", time.Hour)
	if _, err := svc.VerifyAccessToken(other); !errors.Is(err, domain.ErrInvalidToken) {
		t.Errorf("wrong issuer: got %v", err)
	}
	// Expired.
	expired := mustSign(t, jwt.SigningMethodHS256, secret, "medikmu-test", -time.Hour)
	if _, err := svc.VerifyAccessToken(expired); !errors.Is(err, domain.ErrInvalidToken) {
		t.Errorf("expired: got %v", err)
	}
	// alg=none must never be accepted.
	none := mustSign(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, "medikmu-test", time.Hour)
	if _, err := svc.VerifyAccessToken(none); !errors.Is(err, domain.ErrInvalidToken) {
		t.Errorf("alg none: got %v", err)
	}
	if _, err := svc.VerifyAccessToken(""); !errors.Is(err, domain.ErrInvalidToken) {
		t.Errorf("empty: got %v", err)
	}
}

func mustSign(t *testing.T, m jwt.SigningMethod, key any, issuer string, ttl time.Duration) string {
	t.Helper()
	s, err := jwt.NewWithClaims(m, jwt.RegisteredClaims{
		Issuer: issuer, Subject: "user-1",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
	}).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPasswordHashIsSaltedAndNotPlaintext(t *testing.T) {
	st := authtest.NewMemStore()
	svc, _ := service.New(st, service.Config{JWTSecret: secret, AccessTTL: time.Minute, RefreshTTL: time.Hour})
	ctx := context.Background()
	for _, e := range []string{"a@b.co", "c@d.co"} {
		if _, err := svc.Register(ctx, service.RegisterInput{Email: e, Password: "same-password", FullName: "X"}); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := st.UserByEmail(ctx, "a@b.co")
	c, _ := st.UserByEmail(ctx, "c@d.co")
	if !strings.HasPrefix(a.PasswordHash, "$argon2id$") || strings.Contains(a.PasswordHash, "same-password") {
		t.Fatalf("unexpected hash format: %s", a.PasswordHash)
	}
	if a.PasswordHash == c.PasswordHash {
		t.Fatal("identical passwords must produce different hashes (salt)")
	}
}
