package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/sangmul/medikmu-backend/internal/facilities/domain"
	"github.com/sangmul/medikmu-backend/internal/facilities/facilitiestest"
	"github.com/sangmul/medikmu-backend/internal/facilities/service"
	"github.com/sangmul/medikmu-backend/internal/platform/audit"
	"github.com/sangmul/medikmu-backend/internal/platform/authctx"
)

type env struct {
	svc   *service.Service
	store *facilitiestest.MemStore
	audit *audit.Memory
}

func newEnv() *env {
	st, au := facilitiestest.NewMemStore(), &audit.Memory{}
	return &env{svc: service.New(st, au), store: st, audit: au}
}

func personal(userID string) authctx.Principal { return authctx.Principal{UserID: userID} }

func inFacility(userID, facility, role string) authctx.Principal {
	return authctx.Principal{UserID: userID, FacilityID: facility, Role: role}
}

func clinic() service.FacilityInput {
	return service.FacilityInput{Name: "Klinik Sehat Bersama", Kind: "clinic", Phone: "+62 21 555 0100", Address: "Jl. Merdeka 1"}
}

// setup creates an owner (admin) of a new clinic and returns the owner's principal inside it.
func (e *env) setup(t *testing.T) (admin authctx.Principal, facilityID string) {
	t.Helper()
	owner := e.store.AddUser("Dr. Owner", "owner@example.com")
	f, err := e.svc.CreateFacility(context.Background(), personal(owner), clinic())
	if err != nil {
		t.Fatal(err)
	}
	return inFacility(owner, f.ID, domain.RoleAdmin), f.ID
}

func TestCreateFacility_CreatorBecomesAdmin(t *testing.T) {
	e := newEnv()
	owner := e.store.AddUser("Dr. Owner", "owner@example.com")
	f, err := e.svc.CreateFacility(context.Background(), personal(owner), clinic())
	if err != nil {
		t.Fatal(err)
	}
	if f.ID == "" || f.CreatedBy != owner || f.Name != "Klinik Sehat Bersama" {
		t.Fatalf("bad facility: %+v", f)
	}
	if got := e.store.Role(f.ID, owner); got != domain.RoleAdmin {
		t.Fatalf("creator role = %q, want admin", got)
	}
	if got := e.audit.Actions(); fmt.Sprint(got) != "[facility.create]" {
		t.Fatalf("audit = %v", got)
	}
}

func TestCreateFacility_Validation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*service.FacilityInput)
		field  string
	}{
		{"short name", func(i *service.FacilityInput) { i.Name = "ab" }, "name"},
		{"bad kind", func(i *service.FacilityInput) { i.Kind = "pharmacy" }, "kind"},
		{"bad phone", func(i *service.FacilityInput) { i.Phone = "call me maybe" }, "phone"},
		{"short phone", func(i *service.FacilityInput) { i.Phone = "123" }, "phone"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv()
			in := clinic()
			tt.mutate(&in)
			_, err := e.svc.CreateFacility(context.Background(), personal(e.store.AddUser("U", "u@example.com")), in)
			var ve *domain.ValidationError
			if !errors.As(err, &ve) || ve.Fields[tt.field] == "" {
				t.Fatalf("want validation error on %q, got %v", tt.field, err)
			}
		})
	}
}

func TestCreateFacility_CapPerUser(t *testing.T) {
	e := newEnv()
	owner := e.store.AddUser("U", "u@example.com")
	for i := range domain.MaxFacilitiesPerUser {
		if _, err := e.svc.CreateFacility(context.Background(), personal(owner), clinic()); err != nil {
			t.Fatalf("facility %d: %v", i, err)
		}
	}
	if _, err := e.svc.CreateFacility(context.Background(), personal(owner), clinic()); !errors.Is(err, domain.ErrLimitReached) {
		t.Fatalf("want ErrLimitReached, got %v", err)
	}
	// Another user is unaffected.
	other := e.store.AddUser("V", "v@example.com")
	if _, err := e.svc.CreateFacility(context.Background(), personal(other), clinic()); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentAndUpdate_Permissions(t *testing.T) {
	e := newEnv()
	admin, fid := e.setup(t)
	ctx := context.Background()

	// Invite a nurse who accepts: she may read but not edit.
	res, _ := e.svc.Invite(ctx, admin, "nurse@example.com", domain.RoleNurse)
	nurseID := e.store.AddUser("Suster", "nurse@example.com")
	if _, err := e.svc.AcceptInvite(ctx, personal(nurseID), res.Token); err != nil {
		t.Fatal(err)
	}
	nurse := inFacility(nurseID, fid, domain.RoleNurse)

	if f, err := e.svc.Current(ctx, nurse); err != nil || f.ID != fid {
		t.Fatalf("nurse read: %v %+v", err, f)
	}
	if _, err := e.svc.UpdateCurrent(ctx, nurse, service.FacilityInput{Name: "Diubah Perawat"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("nurse update: want ErrForbidden, got %v", err)
	}
	f, err := e.svc.UpdateCurrent(ctx, admin, service.FacilityInput{Name: "Klinik Baru", Kind: "hospital", Phone: "0215550199"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "Klinik Baru" || f.Kind != "clinic" {
		t.Fatalf("name must change, kind must not: %+v", f)
	}
	// A patient session (no facility) cannot read a facility.
	if _, err := e.svc.Current(ctx, personal(nurseID)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("no facility: want ErrForbidden, got %v", err)
	}
}

func TestInviteAndAcceptFlow(t *testing.T) {
	e := newEnv()
	admin, fid := e.setup(t)
	ctx := context.Background()

	res, err := e.svc.Invite(ctx, admin, " Dokter@Example.com ", domain.RoleDoctor)
	if err != nil {
		t.Fatal(err)
	}
	if res.Token == "" || res.Invite.Email != "dokter@example.com" || res.Invite.TokenHash == res.Token {
		t.Fatalf("invite: %+v (token must be returned once and stored only as a hash)", res)
	}

	pending, _ := e.svc.Invites(ctx, admin)
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}

	docID := e.store.AddUser("Dr. Budi", "dokter@example.com")
	inv, err := e.svc.AcceptInvite(ctx, personal(docID), res.Token)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if inv.FacilityID != fid || e.store.Role(fid, docID) != domain.RoleDoctor {
		t.Fatalf("membership not created: %+v role=%q", inv, e.store.Role(fid, docID))
	}
	if pending, _ := e.svc.Invites(ctx, admin); len(pending) != 0 {
		t.Fatalf("accepted invite must not stay pending: %d", len(pending))
	}
	team, _ := e.svc.Team(ctx, admin)
	if len(team) != 2 {
		t.Fatalf("team size = %d, want 2", len(team))
	}
	// Single use.
	if _, err := e.svc.AcceptInvite(ctx, personal(docID), res.Token); !errors.Is(err, domain.ErrInviteInvalid) {
		t.Fatalf("reuse: want ErrInviteInvalid, got %v", err)
	}
}

func TestAccept_IsAllOrNothingAndNeverRevealsWhy(t *testing.T) {
	e := newEnv()
	admin, _ := e.setup(t)
	ctx := context.Background()
	res, _ := e.svc.Invite(ctx, admin, "dokter@example.com", domain.RoleDoctor)

	stranger := e.store.AddUser("Orang Lain", "stranger@example.com")
	for name, tok := range map[string]string{"wrong email": res.Token, "unknown token": "nope", "empty": ""} {
		if _, err := e.svc.AcceptInvite(ctx, personal(stranger), tok); !errors.Is(err, domain.ErrInviteInvalid) {
			t.Errorf("%s: want ErrInviteInvalid (indistinguishable), got %v", name, err)
		}
	}
	// A stranger's failed attempt must not burn the invite.
	docID := e.store.AddUser("Dr. Budi", "dokter@example.com")
	if _, err := e.svc.AcceptInvite(ctx, personal(docID), res.Token); err != nil {
		t.Fatalf("legit accept after failed attempts: %v", err)
	}
	if _, err := e.svc.AcceptInvite(ctx, authctx.Principal{}, res.Token); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("anonymous: want ErrForbidden, got %v", err)
	}
}

func TestInvite_Rules(t *testing.T) {
	e := newEnv()
	admin, _ := e.setup(t)
	ctx := context.Background()

	var ve *domain.ValidationError
	if _, err := e.svc.Invite(ctx, admin, "not-an-email", "doctor"); !errors.As(err, &ve) || ve.Fields["email"] == "" {
		t.Errorf("bad email: %v", err)
	}
	if _, err := e.svc.Invite(ctx, admin, "a@example.com", "superuser"); !errors.As(err, &ve) || ve.Fields["role"] == "" {
		t.Errorf("bad role: %v", err)
	}
	if _, err := e.svc.Invite(ctx, admin, "a@example.com", "doctor"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Invite(ctx, admin, "A@example.com", "nurse"); !errors.Is(err, domain.ErrInvitePending) {
		t.Errorf("duplicate pending: want ErrInvitePending, got %v", err)
	}
	if _, err := e.svc.Invite(ctx, admin, "owner@example.com", "doctor"); !errors.Is(err, domain.ErrAlreadyMember) {
		t.Errorf("already a member: want ErrAlreadyMember, got %v", err)
	}
}

func TestRevokeInvite(t *testing.T) {
	e := newEnv()
	admin, _ := e.setup(t)
	ctx := context.Background()
	res, _ := e.svc.Invite(ctx, admin, "dokter@example.com", domain.RoleDoctor)

	if err := e.svc.RevokeInvite(ctx, admin, res.Invite.ID); err != nil {
		t.Fatal(err)
	}
	docID := e.store.AddUser("Dr. Budi", "dokter@example.com")
	if _, err := e.svc.AcceptInvite(ctx, personal(docID), res.Token); !errors.Is(err, domain.ErrInviteInvalid) {
		t.Fatalf("revoked invite must not be acceptable, got %v", err)
	}
	if err := e.svc.RevokeInvite(ctx, admin, res.Invite.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second revoke: want ErrNotFound, got %v", err)
	}
	// After revoking, a fresh invite to the same email is allowed.
	if _, err := e.svc.Invite(ctx, admin, "dokter@example.com", domain.RoleDoctor); err != nil {
		t.Fatalf("re-invite after revoke: %v", err)
	}
}

func TestAdminOnlyOperations(t *testing.T) {
	e := newEnv()
	admin, fid := e.setup(t)
	ctx := context.Background()
	res, _ := e.svc.Invite(ctx, admin, "doc@example.com", domain.RoleDoctor)
	docID := e.store.AddUser("Dr", "doc@example.com")
	_, _ = e.svc.AcceptInvite(ctx, personal(docID), res.Token)
	doctor := inFacility(docID, fid, domain.RoleDoctor)

	checks := map[string]error{}
	_, checks["team"] = e.svc.Team(ctx, doctor)
	_, checks["invite"] = e.svc.Invite(ctx, doctor, "x@example.com", "staff")
	_, checks["invites"] = e.svc.Invites(ctx, doctor)
	checks["revoke"] = e.svc.RevokeInvite(ctx, doctor, "00000000-0000-4000-8000-000000000001")
	checks["remove"] = e.svc.RemoveMember(ctx, doctor, admin.UserID)
	for op, err := range checks {
		if !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("doctor %s: want ErrForbidden, got %v", op, err)
		}
	}
}

func TestRemoveMember_LastAdminGuard(t *testing.T) {
	e := newEnv()
	admin, fid := e.setup(t)
	ctx := context.Background()

	if err := e.svc.RemoveMember(ctx, admin, admin.UserID); !errors.Is(err, domain.ErrLastAdmin) {
		t.Fatalf("removing the only admin: want ErrLastAdmin, got %v", err)
	}
	// Add a second admin; now one of them can leave.
	res, _ := e.svc.Invite(ctx, admin, "second@example.com", domain.RoleAdmin)
	secondID := e.store.AddUser("Admin Dua", "second@example.com")
	if _, err := e.svc.AcceptInvite(ctx, personal(secondID), res.Token); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RemoveMember(ctx, admin, secondID); err != nil {
		t.Fatalf("remove one of two admins: %v", err)
	}
	if e.store.Role(fid, secondID) != "" {
		t.Fatal("member still present after removal")
	}
	if err := e.svc.RemoveMember(ctx, admin, secondID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("remove twice: want ErrNotFound, got %v", err)
	}
	if err := e.svc.RemoveMember(ctx, admin, "not-a-uuid"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("bad id: want ErrNotFound, got %v", err)
	}
}

func TestTenantIsolation(t *testing.T) {
	e := newEnv()
	adminA, _ := e.setup(t)
	ownerB := e.store.AddUser("Owner B", "b@example.com")
	fB, _ := e.svc.CreateFacility(context.Background(), personal(ownerB), clinic())
	adminB := inFacility(ownerB, fB.ID, domain.RoleAdmin)
	ctx := context.Background()

	resA, _ := e.svc.Invite(ctx, adminA, "x@example.com", domain.RoleStaff)
	// B's admin cannot revoke A's invite, nor see A's team or invites.
	if err := e.svc.RevokeInvite(ctx, adminB, resA.Invite.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-tenant revoke: want ErrNotFound, got %v", err)
	}
	if err := e.svc.RemoveMember(ctx, adminB, adminA.UserID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-tenant remove: want ErrNotFound, got %v", err)
	}
	if invites, _ := e.svc.Invites(ctx, adminB); len(invites) != 0 {
		t.Errorf("B sees A's invites: %v", invites)
	}
	if team, _ := e.svc.Team(ctx, adminB); len(team) != 1 || team[0].UserID != ownerB {
		t.Errorf("B's team leaks: %+v", team)
	}
}

func TestAudit_FailClosedAndNoSecrets(t *testing.T) {
	e := newEnv()
	admin, _ := e.setup(t)
	ctx := context.Background()
	res, _ := e.svc.Invite(ctx, admin, "dokter@example.com", domain.RoleDoctor)
	for _, en := range e.audit.Entries {
		if fmt.Sprint(en) != "" && (containsAny(fmt.Sprint(en), res.Token, "dokter@example.com")) {
			t.Fatalf("audit entry leaks token or email: %+v", en)
		}
	}
	e.audit.Err = errors.New("audit down")
	if _, err := e.svc.Invite(ctx, admin, "lain@example.com", domain.RoleStaff); err == nil {
		t.Fatal("invite must report failure when it cannot be audited")
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if sub != "" && len(s) >= len(sub) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
