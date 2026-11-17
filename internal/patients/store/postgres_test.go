package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sangmul/medikmu-backend/internal/patients/domain"
	"github.com/sangmul/medikmu-backend/internal/patients/store"
)

// newStore runs against a real Postgres (TEST_DATABASE_URL) in a throwaway schema with every
// migration applied, and returns two facility IDs. Skipped when the variable is not set.
func newStore(t *testing.T) (st *store.Postgres, pool *pgxpool.Pool, facA, facB string) {
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
	for _, dst := range []*string{&facA, &facB} {
		if err := pool.QueryRow(ctx, `INSERT INTO facilities (name, kind) VALUES ('Klinik', 'clinic') RETURNING id`).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	return store.New(pool), pool, facA, facB
}

func sample(name string) domain.Patient {
	return domain.Patient{
		FullName: name, Sex: "F", BirthDate: time.Date(1990, 4, 12, 0, 0, 0, 0, time.UTC),
		Phone: "081234567890", PaymentType: "bpjs", BPJSNumber: "0001234567890",
		Allergies: []string{"Penicillin", "Egg"}, ConsentAt: time.Now(),
	}
}

func TestCreateAndGetRoundTrip(t *testing.T) {
	st, _, facA, _ := newStore(t)
	ctx := context.Background()

	in := sample("Siti Aminah")
	in.NIK, in.Email, in.Notes = "3174012345678901", "siti@example.com", "catatan"
	in.GuardianName, in.GuardianRelation = "Budi", "Father"
	created, err := st.Create(ctx, facA, in)
	if err != nil {
		t.Fatal(err)
	}
	if created.MRN != "RM-000001" || created.ID == "" || created.FacilityID != facA || created.CreatedAt.IsZero() {
		t.Fatalf("bad create result: %+v", created)
	}

	got, err := st.Get(ctx, facA, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FullName != "Siti Aminah" || got.NIK != "3174012345678901" || got.Email != "siti@example.com" ||
		got.BirthDate.Format("2006-01-02") != "1990-04-12" || len(got.Allergies) != 2 || got.Allergies[1] != "Egg" ||
		got.BPJSNumber != "0001234567890" || got.GuardianRelation != "Father" || got.Notes != "catatan" {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	// Optional fields stay empty (NULL in the database), and empty lists come back as empty slices.
	bare := sample("Tanpa Opsional")
	bare.Allergies = nil
	created2, err := st.Create(ctx, facA, bare)
	if err != nil {
		t.Fatal(err)
	}
	got2, _ := st.Get(ctx, facA, created2.ID)
	if got2.NIK != "" || got2.Email != "" || got2.Allergies == nil || len(got2.Allergies) != 0 || got2.Conditions == nil {
		t.Fatalf("optional fields: %+v", got2)
	}
}

func TestMRN_ConcurrentCreatesAreUniqueAndGapless(t *testing.T) {
	st, _, facA, facB := newStore(t)
	ctx := context.Background()

	const n = 30
	var wg sync.WaitGroup
	mrns := make(chan string, n)
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := st.Create(ctx, facA, sample(fmt.Sprintf("Pasien %d", i)))
			if err != nil {
				errs <- err
				return
			}
			mrns <- p.MRN
		}()
	}
	wg.Wait()
	close(mrns)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent create failed: %v", err)
	}
	seen := map[string]bool{}
	for m := range mrns {
		if seen[m] {
			t.Fatalf("duplicate MRN %s", m)
		}
		seen[m] = true
	}
	for i := 1; i <= n; i++ {
		if !seen[fmt.Sprintf("RM-%06d", i)] {
			t.Fatalf("gap: RM-%06d missing", i)
		}
	}
	// The counter is per facility.
	if p, _ := st.Create(ctx, facB, sample("Fasilitas B")); p.MRN != "RM-000001" {
		t.Fatalf("facility B must start at RM-000001, got %s", p.MRN)
	}
}

func TestNIK_UniquePerFacilityAndReusableAfterArchive(t *testing.T) {
	st, _, facA, facB := newStore(t)
	ctx := context.Background()
	in := sample("Siti")
	in.NIK = "3174012345678901"

	first, err := st.Create(ctx, facA, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Create(ctx, facA, in); !errors.Is(err, domain.ErrNIKTaken) {
		t.Fatalf("duplicate in same facility: want ErrNIKTaken, got %v", err)
	}
	if _, err := st.Create(ctx, facB, in); err != nil {
		t.Fatalf("same NIK at another facility must be allowed: %v", err)
	}
	// A failed insert must not burn an MRN: the next success is RM-000002, not RM-000003.
	other := sample("Lain")
	if p, err := st.Create(ctx, facA, other); err != nil || p.MRN != "RM-000002" {
		t.Fatalf("MRN after failed insert: %v %+v", err, p)
	}

	if err := st.Archive(ctx, facA, first.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Create(ctx, facA, in); err != nil {
		t.Fatalf("NIK of an archived patient should be reusable: %v", err)
	}
}

func TestTenantIsolation(t *testing.T) {
	st, _, facA, facB := newStore(t)
	ctx := context.Background()
	p, err := st.Create(ctx, facA, sample("Siti"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := st.Get(ctx, facB, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get: want ErrNotFound, got %v", err)
	}
	upd := sample("Dibajak")
	upd.ID = p.ID
	if _, err := st.Update(ctx, facB, upd); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("update: want ErrNotFound, got %v", err)
	}
	if err := st.Archive(ctx, facB, p.ID, time.Now()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("archive: want ErrNotFound, got %v", err)
	}
	if rows, err := st.List(ctx, facB, domain.ListFilter{Limit: 10}); err != nil || len(rows) != 0 {
		t.Errorf("list: %v %d rows", err, len(rows))
	}
	// Unharmed.
	if got, err := st.Get(ctx, facA, p.ID); err != nil || got.FullName != "Siti" {
		t.Fatalf("owner's patient changed: %v %+v", err, got)
	}
}

func TestUpdateKeepsImmutableFields(t *testing.T) {
	st, _, facA, _ := newStore(t)
	ctx := context.Background()
	p, _ := st.Create(ctx, facA, sample("Siti"))

	upd := sample("Siti Putri")
	upd.ID = p.ID
	upd.Allergies = []string{"Dust"}
	upd.PaymentType, upd.BPJSNumber = "private", ""
	got, err := st.Update(ctx, facA, upd)
	if err != nil {
		t.Fatal(err)
	}
	if got.FullName != "Siti Putri" || got.PaymentType != "private" || got.BPJSNumber != "" || len(got.Allergies) != 1 {
		t.Fatalf("not updated: %+v", got)
	}
	if got.MRN != p.MRN || !got.ConsentAt.Equal(p.ConsentAt) || !got.CreatedAt.Equal(p.CreatedAt) || !got.UpdatedAt.After(p.UpdatedAt) {
		t.Fatalf("immutable fields/updated_at wrong: before %+v after %+v", p, got)
	}
}

func TestList_KeysetPaginationAndSearch(t *testing.T) {
	st, _, facA, _ := newStore(t)
	ctx := context.Background()
	names := []string{"Andi Wijaya", "Budi Santoso", "Citra Dewi", "Dewi Lestari", "Eko Prasetyo", "100% Sehat", "Under_score"}
	for _, n := range names {
		if _, err := st.Create(ctx, facA, sample(n)); err != nil {
			t.Fatal(err)
		}
	}

	// Walk pages of 3 using the same cursor logic the service uses.
	var all []string
	var cursor *domain.Cursor
	for pages := 0; pages < 10; pages++ {
		rows, err := st.List(ctx, facA, domain.ListFilter{Cursor: cursor, Limit: 3})
		if err != nil {
			t.Fatal(err)
		}
		n := min(len(rows), 3)
		for _, p := range rows[:n] {
			all = append(all, p.FullName)
		}
		if len(rows) <= 3 {
			break
		}
		cursor = &domain.Cursor{CreatedAt: rows[2].CreatedAt, ID: rows[2].ID}
	}
	if len(all) != len(names) {
		t.Fatalf("want %d patients across pages, got %d: %v", len(names), len(all), all)
	}
	if all[0] != "Under_score" || all[len(all)-1] != "Andi Wijaya" {
		t.Fatalf("order must be newest first: %v", all)
	}
	seen := map[string]bool{}
	for _, n := range all {
		if seen[n] {
			t.Fatalf("duplicate across pages: %s", n)
		}
		seen[n] = true
	}

	search := func(q string) int {
		rows, err := st.List(ctx, facA, domain.ListFilter{Query: q, Limit: 50})
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		return len(rows)
	}
	if got := search("dewi"); got != 2 {
		t.Errorf("name search 'dewi': want 2, got %d", got)
	}
	if got := search("rm-000003"); got != 1 {
		t.Errorf("MRN search: want 1, got %d", got)
	}
	if got := search("0812345"); got != len(names) {
		t.Errorf("phone search: want %d, got %d", len(names), got)
	}
	// LIKE wildcards in user input are literals, not patterns.
	if got := search("%"); got != 1 {
		t.Errorf("search '%%' must match only the name containing a literal %%, got %d", got)
	}
	if got := search("_"); got != 1 {
		t.Errorf("search '_' must match only the name containing a literal _, got %d", got)
	}
	if got := search(`\`); got != 0 {
		t.Errorf(`search '\' : want 0, got %d`, got)
	}
	if got := search("'; DROP TABLE patients; --"); got != 0 {
		t.Errorf("injection string: want 0 results, got %d", got)
	}
}

func TestSchemaConstraints(t *testing.T) {
	_, pool, facA, _ := newStore(t)
	ctx := context.Background()
	// The database itself rejects values the service would never send: defense in depth.
	_, err := pool.Exec(ctx, `INSERT INTO patients (facility_id, mrn, full_name, sex, birth_date, phone, payment_type, consent_at)
		VALUES ($1, 'RM-X', 'X', 'Z', '2000-01-01', '0812', 'bpjs', now())`, facA)
	if err == nil {
		t.Error("sex='Z' must violate the CHECK constraint")
	}
	_, err = pool.Exec(ctx, `INSERT INTO patients (facility_id, mrn, full_name, sex, birth_date, phone, payment_type, consent_at)
		VALUES ($1, 'RM-X', 'X', 'F', '2000-01-01', '0812', 'cash', now())`, facA)
	if err == nil {
		t.Error("payment_type='cash' must violate the CHECK constraint")
	}
}
