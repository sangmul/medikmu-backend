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

	"github.com/sangmul/medikmu-backend/internal/family/domain"
	"github.com/sangmul/medikmu-backend/internal/family/store"
)

// newStore runs against a real Postgres (TEST_DATABASE_URL) in a throwaway schema with every migration
// applied, and returns two account IDs. Skipped when the variable is not set.
func newStore(t *testing.T) (st *store.Postgres, pool *pgxpool.Pool, ana, budi string) {
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
	pool, err = pgxpool.NewWithConfig(ctx, cfg)
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
	ids := make([]string, 2)
	for i, email := range []string{"ana@example.com", "budi@example.com"} {
		if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, full_name) VALUES ($1, 'x', 'U') RETURNING id`, email).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	return store.New(pool), pool, ids[0], ids[1]
}

func aira() domain.Member {
	return domain.Member{
		FullName: "Aira Putri", Relation: "child", Sex: "F", AvatarTone: "teal",
		BirthDate: time.Date(2023, 5, 10, 0, 0, 0, 0, time.UTC),
	}
}

func TestRoundTripWithAndWithoutOptionalFields(t *testing.T) {
	st, _, ana, _ := newStore(t)
	ctx := context.Background()

	full := aira()
	full.NIK, full.BPJSNumber, full.BPJSHolderName, full.BPJSClass = "3174012345678901", "0001234567890", "AIRA PUTRI", 2
	created, err := st.Create(ctx, ana, full)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(ctx, ana, created.ID)
	if err != nil || got.NIK != full.NIK || got.BPJSNumber != full.BPJSNumber || got.BPJSClass != 2 ||
		got.BirthDate.Format("2006-01-02") != "2023-05-10" || got.AvatarTone != "teal" || got.UserID != ana {
		t.Fatalf("round trip: %v %+v", err, got)
	}

	bare, err := st.Create(ctx, ana, aira())
	if err != nil {
		t.Fatal(err)
	}
	if bare.NIK != "" || bare.BPJSNumber != "" || bare.BPJSClass != 0 {
		t.Fatalf("optional fields must be empty: %+v", bare)
	}
	list, _ := st.List(ctx, ana)
	if len(list) != 2 || list[0].ID != created.ID {
		t.Fatalf("list order (oldest first): %+v", list)
	}
}

func TestOwnershipIsolation(t *testing.T) {
	st, _, ana, budi := newStore(t)
	ctx := context.Background()
	m, _ := st.Create(ctx, ana, aira())

	if _, err := st.Get(ctx, budi, m.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get: %v", err)
	}
	upd := aira()
	upd.ID, upd.FullName = m.ID, "Dibajak"
	if _, err := st.Update(ctx, budi, upd); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("update: %v", err)
	}
	if _, err := st.SetNIK(ctx, budi, m.ID, "3174012345678901"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("set nik: %v", err)
	}
	if err := st.Delete(ctx, budi, m.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("delete: %v", err)
	}
	if list, err := st.List(ctx, budi); err != nil || len(list) != 0 {
		t.Errorf("list: %v %d", err, len(list))
	}
	if got, err := st.Get(ctx, ana, m.ID); err != nil || got.FullName != "Aira Putri" {
		t.Fatalf("owner's member changed: %v %+v", err, got)
	}
}

func TestNIKUniquePerAccount(t *testing.T) {
	st, _, ana, budi := newStore(t)
	ctx := context.Background()
	a := aira()
	a.NIK = "3174012345678901"
	first, err := st.Create(ctx, ana, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Create(ctx, ana, a); !errors.Is(err, domain.ErrNIKTaken) {
		t.Fatalf("dup on create: want ErrNIKTaken, got %v", err)
	}
	other, _ := st.Create(ctx, ana, aira())
	if _, err := st.SetNIK(ctx, ana, other.ID, "3174012345678901"); !errors.Is(err, domain.ErrNIKTaken) {
		t.Fatalf("dup on set: want ErrNIKTaken, got %v", err)
	}
	// Re-saving a member's own NIK is not a conflict; two members without a NIK do not collide.
	if got, err := st.SetNIK(ctx, ana, first.ID, "3174012345678901"); err != nil || got.NIK != "3174012345678901" {
		t.Fatalf("own NIK: %v", err)
	}
	if _, err := st.Create(ctx, ana, aira()); err != nil {
		t.Fatalf("members without NIK: %v", err)
	}
	if _, err := st.Create(ctx, budi, a); err != nil {
		t.Fatalf("same NIK on another account: %v", err)
	}
	// Clearing a NIK frees it.
	if _, err := st.SetNIK(ctx, ana, first.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetNIK(ctx, ana, other.ID, "3174012345678901"); err != nil {
		t.Fatalf("freed NIK should be reusable: %v", err)
	}
}

func TestUpdateAndDelete(t *testing.T) {
	st, _, ana, _ := newStore(t)
	ctx := context.Background()
	m, _ := st.Create(ctx, ana, aira())

	upd := aira()
	upd.ID, upd.FullName, upd.AvatarTone, upd.BPJSNumber, upd.BPJSClass = m.ID, "Aira Baru", "blue", "0001234567890", 3
	got, err := st.Update(ctx, ana, upd)
	if err != nil || got.FullName != "Aira Baru" || got.AvatarTone != "blue" || got.BPJSClass != 3 || !got.UpdatedAt.After(m.UpdatedAt) {
		t.Fatalf("update: %v %+v", err, got)
	}
	if err := st.Delete(ctx, ana, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, ana, m.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestCapUnderConcurrency(t *testing.T) {
	st, _, ana, _ := newStore(t)
	ctx := context.Background()
	var created atomic.Int32
	var wg sync.WaitGroup
	for range domain.MaxMembersPerUser + 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := st.Create(ctx, ana, aira()); err == nil {
				created.Add(1)
			} else if !errors.Is(err, domain.ErrLimitReached) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if created.Load() != domain.MaxMembersPerUser {
		t.Fatalf("created %d members, want exactly the cap %d", created.Load(), domain.MaxMembersPerUser)
	}
}

func TestSchemaConstraintsAndCascade(t *testing.T) {
	st, pool, ana, _ := newStore(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO family_members (user_id, full_name, relation, sex, birth_date, bpjs_class)
		VALUES ($1, 'X', 'child', 'F', '2020-01-01', 9)`, ana); err == nil {
		t.Error("bpjs_class=9 must violate the CHECK constraint")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO family_members (user_id, full_name, relation, sex, birth_date)
		VALUES ($1, 'X', 'cousin', 'F', '2020-01-01')`, ana); err == nil {
		t.Error("relation='cousin' must violate the CHECK constraint")
	}
	// Deleting the account removes its family data (right to erasure).
	if _, err := st.Create(ctx, ana, aira()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, ana); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM family_members`).Scan(&n)
	if n != 0 {
		t.Fatalf("family members must cascade-delete with the account, %d left", n)
	}
}
