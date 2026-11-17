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

	"github.com/sangmul/medikmu-backend/internal/auth/domain"
	"github.com/sangmul/medikmu-backend/internal/auth/store"
)

// newStore runs against a real Postgres (TEST_DATABASE_URL), in a throwaway schema with all
// migrations applied. Skipped when the variable is not set.
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

func TestUsers(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	u, err := st.CreateUser(ctx, domain.User{Email: "budi@example.com", Phone: "0812", PasswordHash: "h", FullName: "Budi"})
	if err != nil || u.ID == "" {
		t.Fatalf("create: %v %+v", err, u)
	}
	if _, err := st.CreateUser(ctx, domain.User{Email: "budi@example.com", PasswordHash: "h", FullName: "X"}); !errors.Is(err, domain.ErrEmailTaken) {
		t.Fatalf("dup email: want ErrEmailTaken, got %v", err)
	}
	if _, err := st.CreateUser(ctx, domain.User{Email: "lain@example.com", Phone: "0812", PasswordHash: "h", FullName: "X"}); !errors.Is(err, domain.ErrPhoneTaken) {
		t.Fatalf("dup phone: want ErrPhoneTaken, got %v", err)
	}
	// Two users without a phone must not collide.
	for _, e := range []string{"a@example.com", "b@example.com"} {
		if _, err := st.CreateUser(ctx, domain.User{Email: e, PasswordHash: "h", FullName: "N"}); err != nil {
			t.Fatalf("phoneless user %s: %v", e, err)
		}
	}

	got, err := st.UserByEmail(ctx, "budi@example.com")
	if err != nil || got.ID != u.ID || got.Phone != "0812" {
		t.Fatalf("by email: %v %+v", err, got)
	}
	if _, err := st.UserByID(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UserByEmail(ctx, "nobody@example.com"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestMemberships(t *testing.T) {
	st, pool := newStore(t)
	ctx := context.Background()
	u, _ := st.CreateUser(ctx, domain.User{Email: "dokter@example.com", PasswordHash: "h", FullName: "Dr"})

	var facilityID string
	if err := pool.QueryRow(ctx, `INSERT INTO facilities (name, kind) VALUES ('Klinik A', 'clinic') RETURNING id`).Scan(&facilityID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO memberships (user_id, facility_id, role) VALUES ($1, $2, 'doctor')`, u.ID, facilityID); err != nil {
		t.Fatal(err)
	}
	ms, err := st.Memberships(ctx, u.ID)
	if err != nil || len(ms) != 1 || ms[0].FacilityID != facilityID || ms[0].Role != "doctor" {
		t.Fatalf("memberships: %v %+v", err, ms)
	}
	if none, err := st.Memberships(ctx, "00000000-0000-0000-0000-000000000000"); err != nil || len(none) != 0 {
		t.Fatalf("unknown user: %v %+v", err, none)
	}
}

func TestRefreshTokens(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()
	u, _ := st.CreateUser(ctx, domain.User{Email: "budi@example.com", PasswordHash: "h", FullName: "Budi"})
	family := "11111111-1111-4111-8111-111111111111"

	for _, h := range []string{"hash-1", "hash-2"} {
		if err := st.InsertRefreshToken(ctx, domain.RefreshToken{
			UserID: u.ID, FamilyID: family, TokenHash: h, ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	tok, err := st.RefreshTokenByHash(ctx, "hash-1")
	if err != nil || tok.UsedAt != nil || tok.RevokedAt != nil || tok.FacilityID != "" {
		t.Fatalf("lookup: %v %+v", err, tok)
	}
	if _, err := st.RefreshTokenByHash(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}

	// Atomic: of 10 concurrent attempts on one token exactly one wins.
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := st.MarkRefreshTokenUsed(ctx, tok.ID, time.Now()); err == nil && ok {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("want exactly 1 winner, got %d", wins.Load())
	}

	if err := st.RevokeFamily(ctx, family, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"hash-1", "hash-2"} {
		if got, _ := st.RefreshTokenByHash(ctx, h); got.RevokedAt == nil {
			t.Fatalf("%s should be revoked", h)
		}
	}
}
