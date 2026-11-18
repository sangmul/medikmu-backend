package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sangmul/medikmu-backend/internal/facilities/domain"
	"github.com/sangmul/medikmu-backend/internal/facilities/store"
)

// newStore runs against a real Postgres (TEST_DATABASE_URL) in a throwaway schema with every migration
// applied. Skipped when the variable is not set.
func newStore(t *testing.T) (*store.Postgres, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()

	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("t_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.MaxConns = 20
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	files, err := filepath.Glob("../../../migrations/*.up.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found: %v", err)
	}
	sort.Strings(files)
	for _, f := range files {
		sql, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", f, err)
		}
	}
	return store.New(pool), pool
}

func addUser(t *testing.T, pool *pgxpool.Pool, name, email string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, password_hash, full_name) VALUES ($1, 'x', $2) RETURNING id`, email, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func newFacility(t *testing.T, st *store.Postgres, owner string) domain.Facility {
	t.Helper()
	f, err := st.CreateFacility(context.Background(), domain.Facility{Name: "Klinik Uji", Kind: "clinic", Phone: "0215550100", CreatedBy: owner})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func invite(email, role, hash string, ttl time.Duration, facilityID string) domain.Invite {
	return domain.Invite{FacilityID: facilityID, Email: email, Role: role, TokenHash: hash, ExpiresAt: time.Now().Add(ttl)}
}

func TestCreateFacility_AdminAndCap(t *testing.T) {
	st, pool := newStore(t)
	ctx := context.Background()
	owner := addUser(t, pool, "Owner", "owner@example.com")

	f := newFacility(t, st, owner)
	got, err := st.FacilityByID(ctx, f.ID)
	if err != nil || got.Name != "Klinik Uji" || got.Phone != "0215550100" || got.CreatedBy != owner {
		t.Fatalf("round trip: %v %+v", err, got)
	}
	members, _ := st.Members(ctx, f.ID)
	if len(members) != 1 || members[0].UserID != owner || members[0].Role != "admin" || members[0].Email != "owner@example.com" {
		t.Fatalf("creator must be the admin member: %+v", members)
	}

	// Concurrent creates must never exceed the cap (advisory lock).
	var created atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := st.CreateFacility(ctx, domain.Facility{Name: "Klinik Paralel", Kind: "clinic", CreatedBy: owner}); err == nil {
				created.Add(1)
			} else if !errors.Is(err, domain.ErrLimitReached) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if total := 1 + int(created.Load()); total != domain.MaxFacilitiesPerUser {
		t.Fatalf("user ended with %d facilities, want exactly the cap %d", total, domain.MaxFacilitiesPerUser)
	}
}

func TestUpdateFacility(t *testing.T) {
	st, pool := newStore(t)
	f := newFacility(t, st, addUser(t, pool, "Owner", "owner@example.com"))
	got, err := st.UpdateFacility(context.Background(), domain.Facility{ID: f.ID, Name: "Nama Baru", Phone: "", Address: "Alamat"})
	if err != nil || got.Name != "Nama Baru" || got.Kind != "clinic" || got.Address != "Alamat" || !got.UpdatedAt.After(f.UpdatedAt) {
		t.Fatalf("update: %v %+v", err, got)
	}
	if _, err := st.UpdateFacility(context.Background(), domain.Facility{ID: "00000000-0000-4000-8000-000000000000", Name: "x"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown facility: want ErrNotFound, got %v", err)
	}
}

func TestInvites_PendingRulesAndExpiry(t *testing.T) {
	st, pool := newStore(t)
	ctx := context.Background()
	f := newFacility(t, st, addUser(t, pool, "Owner", "owner@example.com"))

	if _, err := st.CreateInvite(ctx, invite("owner@example.com", "doctor", "h0", time.Hour, f.ID)); !errors.Is(err, domain.ErrAlreadyMember) {
		t.Fatalf("inviting a member: want ErrAlreadyMember, got %v", err)
	}
	first, err := st.CreateInvite(ctx, invite("dokter@example.com", "doctor", "h1", time.Hour, f.ID))
	if err != nil || first.ID == "" {
		t.Fatal(err)
	}
	if _, err := st.CreateInvite(ctx, invite("dokter@example.com", "nurse", "h2", time.Hour, f.ID)); !errors.Is(err, domain.ErrInvitePending) {
		t.Fatalf("second pending: want ErrInvitePending, got %v", err)
	}
	// The same email may be invited by a different facility.
	other := newFacility(t, st, addUser(t, pool, "Owner2", "owner2@example.com"))
	if _, err := st.CreateInvite(ctx, invite("dokter@example.com", "doctor", "h3", time.Hour, other.ID)); err != nil {
		t.Fatalf("other facility: %v", err)
	}
	// Expired invites are not listed; revoked ones neither, and a revoked email can be invited again.
	if _, err := st.CreateInvite(ctx, invite("lama@example.com", "staff", "h4", -time.Hour, f.ID)); err != nil {
		t.Fatal(err)
	}
	pending, _ := st.PendingInvites(ctx, f.ID, time.Now())
	if len(pending) != 1 || pending[0].Email != "dokter@example.com" {
		t.Fatalf("pending: %+v", pending)
	}
	if err := st.RevokeInvite(ctx, f.ID, first.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeInvite(ctx, other.ID, first.ID, time.Now()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-facility/second revoke: want ErrNotFound, got %v", err)
	}
	if _, err := st.CreateInvite(ctx, invite("dokter@example.com", "doctor", "h5", time.Hour, f.ID)); err != nil {
		t.Fatalf("re-invite after revoke: %v", err)
	}
	if got, err := st.InviteByHash(ctx, "h5"); err != nil || got.Email != "dokter@example.com" {
		t.Fatalf("by hash: %v %+v", err, got)
	}
	if _, err := st.InviteByHash(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown hash: want ErrNotFound, got %v", err)
	}
}

func TestAcceptInvite_AtomicAndSingleUse(t *testing.T) {
	st, pool := newStore(t)
	ctx := context.Background()
	f := newFacility(t, st, addUser(t, pool, "Owner", "owner@example.com"))
	doc := addUser(t, pool, "Dr", "dokter@example.com")
	inv, _ := st.CreateInvite(ctx, invite("dokter@example.com", "doctor", "h1", time.Hour, f.ID))

	// 10 concurrent accepts of the same invite: exactly one wins.
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := st.AcceptInvite(ctx, inv.ID, doc, time.Now()); err == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("want exactly 1 successful accept, got %d", wins.Load())
	}
	members, _ := st.Members(ctx, f.ID)
	if len(members) != 2 {
		t.Fatalf("members = %d, want 2 (no duplicate membership)", len(members))
	}

	// Expired invite cannot be accepted.
	exp, _ := st.CreateInvite(ctx, invite("late@example.com", "staff", "h2", -time.Minute, f.ID))
	late := addUser(t, pool, "Late", "late@example.com")
	if _, err := st.AcceptInvite(ctx, exp.ID, late, time.Now()); !errors.Is(err, domain.ErrInviteInvalid) {
		t.Fatalf("expired: want ErrInviteInvalid, got %v", err)
	}
}

func TestAcceptInvite_AlreadyMemberRollsBackAndKeepsInvitePending(t *testing.T) {
	st, pool := newStore(t)
	ctx := context.Background()
	f := newFacility(t, st, addUser(t, pool, "Owner", "owner@example.com"))
	// The invite is for an email that is not a member at creation time...
	inv, _ := st.CreateInvite(ctx, invite("dokter@example.com", "doctor", "h1", time.Hour, f.ID))
	// ...but the account becomes a member by another route before accepting.
	doc := addUser(t, pool, "Dr", "dokter@example.com")
	if _, err := pool.Exec(ctx, `INSERT INTO memberships (user_id, facility_id, role) VALUES ($1, $2, 'nurse')`, doc, f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AcceptInvite(ctx, inv.ID, doc, time.Now()); !errors.Is(err, domain.ErrAlreadyMember) {
		t.Fatalf("want ErrAlreadyMember, got %v", err)
	}
	if pending, _ := st.PendingInvites(ctx, f.ID, time.Now()); len(pending) != 1 {
		t.Fatalf("invite must stay pending after the rolled-back accept: %+v", pending)
	}
}

func TestRemoveMember_LastAdminUnderConcurrency(t *testing.T) {
	st, pool := newStore(t)
	ctx := context.Background()
	a1 := addUser(t, pool, "Admin1", "a1@example.com")
	f := newFacility(t, st, a1)
	a2 := addUser(t, pool, "Admin2", "a2@example.com")
	inv, _ := st.CreateInvite(ctx, invite("a2@example.com", "admin", "h1", time.Hour, f.ID))
	if _, err := st.AcceptInvite(ctx, inv.ID, a2, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Two admins try to remove each other at the same time: one must lose, a facility never ends up admin-less.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, target := range []string{a1, a2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- st.RemoveMember(ctx, f.ID, target)
		}()
	}
	wg.Wait()
	close(results)
	var ok, lastAdmin int
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, domain.ErrLastAdmin):
			lastAdmin++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || lastAdmin != 1 {
		t.Fatalf("want one removal and one ErrLastAdmin, got ok=%d lastAdmin=%d", ok, lastAdmin)
	}
	if members, _ := st.Members(ctx, f.ID); len(members) != 1 || members[0].Role != "admin" {
		t.Fatalf("one admin must remain: %+v", members)
	}
	if err := st.RemoveMember(ctx, f.ID, "00000000-0000-4000-8000-000000000000"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown member: want ErrNotFound, got %v", err)
	}
}

func TestUserEmailAndMembershipUniqueness(t *testing.T) {
	st, pool := newStore(t)
	ctx := context.Background()
	u := addUser(t, pool, "U", "u@example.com")
	if e, err := st.UserEmail(ctx, u); err != nil || e != "u@example.com" {
		t.Fatalf("email: %v %q", err, e)
	}
	if _, err := st.UserEmail(ctx, "00000000-0000-4000-8000-000000000000"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown user: want ErrNotFound, got %v", err)
	}
	f := newFacility(t, st, u)
	// One role per user per facility, enforced by the database.
	if _, err := pool.Exec(ctx, `INSERT INTO memberships (user_id, facility_id, role) VALUES ($1, $2, 'doctor')`, u, f.ID); err == nil {
		t.Fatal("a second role for the same user in the same facility must be rejected")
	}
}
