package service_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sangmul/medikmu-backend/internal/patients/domain"
	"github.com/sangmul/medikmu-backend/internal/patients/patientstest"
	"github.com/sangmul/medikmu-backend/internal/patients/service"
	"github.com/sangmul/medikmu-backend/internal/platform/audit"
	"github.com/sangmul/medikmu-backend/internal/platform/authctx"
)

const (
	facA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	facB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

func who(facility, role string) authctx.Principal {
	return authctx.Principal{UserID: "user-1", FacilityID: facility, Role: role}
}

type env struct {
	svc   *service.Service
	store *patientstest.MemStore
	audit *audit.Memory
}

func newEnv() *env {
	st, au := patientstest.NewMemStore(), &audit.Memory{}
	return &env{svc: service.New(st, au), store: st, audit: au}
}

// adult returns a valid registration for an adult patient.
func adult() service.CreateInput {
	return service.CreateInput{Consent: true, Input: service.Input{
		FullName: "Siti Aminah", Sex: "F", BirthDate: "1990-04-12", Phone: "0812-3456-7890",
		PaymentType: "bpjs", BPJSNumber: "0001234567890",
	}}
}

func TestCreate_AssignsSequentialMRNPerFacility(t *testing.T) {
	e, ctx := newEnv(), context.Background()
	for i, fac := range []string{facA, facA, facB} {
		in := adult()
		in.FullName = fmt.Sprintf("Pasien %d", i)
		p, err := e.svc.Create(ctx, who(fac, "doctor"), in)
		if err != nil {
			t.Fatal(err)
		}
		want := map[int]string{0: "RM-000001", 1: "RM-000002", 2: "RM-000001"}[i]
		if p.MRN != want {
			t.Errorf("patient %d: MRN = %s, want %s (counter is per facility)", i, p.MRN, want)
		}
	}
}

func TestCreate_NormalizesInput(t *testing.T) {
	e := newEnv()
	in := adult()
	in.NIK = "3174 0123-4567 8901"
	in.Email = " Siti@Example.COM "
	in.Allergies = []string{" Penicillin", "penicillin", "", "Egg"}
	p, err := e.svc.Create(context.Background(), who(facA, "nurse"), in)
	if err != nil {
		t.Fatal(err)
	}
	if p.NIK != "3174012345678901" || p.Phone != "081234567890" || p.Email != "siti@example.com" || p.BPJSNumber != "0001234567890" {
		t.Fatalf("not normalized: %+v", p)
	}
	if len(p.Allergies) != 2 || p.Allergies[0] != "Penicillin" || p.Allergies[1] != "Egg" {
		t.Fatalf("allergies not cleaned/deduped: %v", p.Allergies)
	}
	if p.CreatedBy != "user-1" || p.ConsentAt.IsZero() {
		t.Fatalf("missing created_by/consent: %+v", p)
	}
}

func TestCreate_Validation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*service.CreateInput)
		field  string
	}{
		{"short name", func(i *service.CreateInput) { i.FullName = "Al" }, "full_name"},
		{"bad sex", func(i *service.CreateInput) { i.Sex = "X" }, "sex"},
		{"bad date format", func(i *service.CreateInput) { i.BirthDate = "12/04/1990" }, "birth_date"},
		{"future date", func(i *service.CreateInput) { i.BirthDate = "2999-01-01" }, "birth_date"},
		{"ancient date", func(i *service.CreateInput) { i.BirthDate = "1800-01-01" }, "birth_date"},
		{"short nik", func(i *service.CreateInput) { i.NIK = "12345" }, "nik"},
		{"letters in nik", func(i *service.CreateInput) { i.NIK = "31740123456789ab" }, "nik"},
		{"short phone", func(i *service.CreateInput) { i.Phone = "0812" }, "phone"},
		{"bad email", func(i *service.CreateInput) { i.Email = "nope" }, "email"},
		{"bad payment", func(i *service.CreateInput) { i.PaymentType = "cash" }, "payment_type"},
		{"short bpjs", func(i *service.CreateInput) { i.BPJSNumber = "123" }, "bpjs_number"},
		{"bpjs on private", func(i *service.CreateInput) { i.PaymentType = "private" }, "bpjs_number"},
		{"too many allergies", func(i *service.CreateInput) {
			for n := range 25 {
				i.Allergies = append(i.Allergies, fmt.Sprintf("a%d", n))
			}
		}, "allergies"},
		{"no consent", func(i *service.CreateInput) { i.Consent = false }, "consent"},
		{"child needs guardian", func(i *service.CreateInput) { i.BirthDate = "2024-01-01"; i.GuardianName = "" }, "guardian_name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv()
			in := adult()
			tt.mutate(&in)
			_, err := e.svc.Create(context.Background(), who(facA, "doctor"), in)
			var ve *domain.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want ValidationError, got %v", err)
			}
			if _, ok := ve.Fields[tt.field]; !ok {
				t.Fatalf("want field %q, got %v", tt.field, ve.Fields)
			}
			if got := e.audit.Actions(); len(got) != 0 {
				t.Fatalf("a rejected create must not be audited as a success: %v", got)
			}
		})
	}
}

func TestCreate_ChildWithGuardianOK_AdultWithoutGuardianOK(t *testing.T) {
	e, ctx := newEnv(), context.Background()
	child := adult()
	child.BirthDate, child.GuardianName = "2024-01-01", "Budi Santoso"
	if _, err := e.svc.Create(ctx, who(facA, "doctor"), child); err != nil {
		t.Fatalf("child with guardian: %v", err)
	}
	if _, err := e.svc.Create(ctx, who(facA, "doctor"), adult()); err != nil {
		t.Fatalf("adult without guardian: %v", err)
	}
}

func TestCreate_DuplicateNIKPerFacility(t *testing.T) {
	e, ctx := newEnv(), context.Background()
	in := adult()
	in.NIK = "3174012345678901"
	if _, err := e.svc.Create(ctx, who(facA, "doctor"), in); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(ctx, who(facA, "doctor"), in); !errors.Is(err, domain.ErrNIKTaken) {
		t.Fatalf("same facility: want ErrNIKTaken, got %v", err)
	}
	// The same person may be a patient at another facility.
	if _, err := e.svc.Create(ctx, who(facB, "doctor"), in); err != nil {
		t.Fatalf("other facility: %v", err)
	}
}

func TestTenantIsolation(t *testing.T) {
	e, ctx := newEnv(), context.Background()
	p, err := e.svc.Create(ctx, who(facA, "admin"), adult())
	if err != nil {
		t.Fatal(err)
	}
	other := who(facB, "admin")

	if _, err := e.svc.Get(ctx, other, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get across facilities: want ErrNotFound, got %v", err)
	}
	if _, err := e.svc.Update(ctx, other, p.ID, adult().Input); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("update across facilities: want ErrNotFound, got %v", err)
	}
	if err := e.svc.Archive(ctx, other, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("archive across facilities: want ErrNotFound, got %v", err)
	}
	page, err := e.svc.List(ctx, other, "", nil, 10)
	if err != nil || len(page.Patients) != 0 {
		t.Errorf("list across facilities: %v %+v", err, page)
	}
	// Still intact for the owner.
	if _, err := e.svc.Get(ctx, who(facA, "admin"), p.ID); err != nil {
		t.Fatalf("owner lost the patient: %v", err)
	}
}

func TestRoles(t *testing.T) {
	tests := []struct {
		role                      string
		read, write, archive, bad bool
	}{
		{"admin", true, true, true, false},
		{"doctor", true, true, false, false},
		{"nurse", true, true, false, false},
		{"staff", true, true, false, false},
		{"pharmacist", true, false, false, false},
		{"finance", true, false, false, false},
		{"hacker", false, false, false, true},
		{"", false, false, false, true},
	}
	for _, tt := range tests {
		t.Run("role="+tt.role, func(t *testing.T) {
			e, ctx := newEnv(), context.Background()
			seed, _ := e.svc.Create(ctx, who(facA, "admin"), adult())
			p := who(facA, tt.role)

			_, errRead := e.svc.Get(ctx, p, seed.ID)
			in := adult()
			in.NIK = "3174012345678901"
			_, errWrite := e.svc.Create(ctx, p, in)
			errArchive := e.svc.Archive(ctx, p, seed.ID)

			check := func(op string, err error, allowed bool) {
				if allowed && err != nil {
					t.Errorf("%s: want allowed, got %v", op, err)
				}
				if !allowed && !errors.Is(err, domain.ErrForbidden) {
					t.Errorf("%s: want ErrForbidden, got %v", op, err)
				}
			}
			check("read", errRead, tt.read)
			check("write", errWrite, tt.write)
			check("archive", errArchive, tt.archive)
		})
	}
}

func TestNoFacilityContextIsForbidden(t *testing.T) {
	e, ctx := newEnv(), context.Background()
	patientOnly := authctx.Principal{UserID: "u"} // e.g. a mobile patient session
	if _, err := e.svc.List(ctx, patientOnly, "", nil, 10); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
	if _, err := e.svc.Create(ctx, patientOnly, adult()); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestInvalidIDIsNotFound(t *testing.T) {
	e := newEnv()
	for _, id := range []string{"", "abc", "1; DROP TABLE patients", "../etc/passwd"} {
		if _, err := e.svc.Get(context.Background(), who(facA, "admin"), id); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("id %q: want ErrNotFound, got %v", id, err)
		}
	}
}

func TestUpdate_ReplacesFieldsKeepsIdentity(t *testing.T) {
	e, ctx := newEnv(), context.Background()
	p, _ := e.svc.Create(ctx, who(facA, "doctor"), adult())

	in := adult().Input
	in.FullName, in.Phone = "Siti Aminah Putri", "+6281299998888"
	got, err := e.svc.Update(ctx, who(facA, "nurse"), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.FullName != "Siti Aminah Putri" || got.Phone != "+6281299998888" {
		t.Fatalf("not updated: %+v", got)
	}
	if got.ID != p.ID || got.MRN != p.MRN || got.CreatedBy != p.CreatedBy || !got.ConsentAt.Equal(p.ConsentAt) {
		t.Fatalf("identity/consent must not change: %+v vs %+v", got, p)
	}
}

func TestArchive_HidesPatient(t *testing.T) {
	e, ctx := newEnv(), context.Background()
	p, _ := e.svc.Create(ctx, who(facA, "doctor"), adult())
	if err := e.svc.Archive(ctx, who(facA, "admin"), p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Get(ctx, who(facA, "admin"), p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("archived patient must be hidden, got %v", err)
	}
	if err := e.svc.Archive(ctx, who(facA, "admin"), p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second archive: want ErrNotFound, got %v", err)
	}
	// Its NIK can be registered again.
	in := adult()
	in.NIK = "3174012345678901"
	first, _ := e.svc.Create(ctx, who(facA, "doctor"), in)
	_ = e.svc.Archive(ctx, who(facA, "admin"), first.ID)
	if _, err := e.svc.Create(ctx, who(facA, "doctor"), in); err != nil {
		t.Fatalf("NIK of an archived patient should be reusable: %v", err)
	}
}

func TestList_PaginationAndSearch(t *testing.T) {
	e, ctx := newEnv(), context.Background()
	names := []string{"Andi Wijaya", "Budi Santoso", "Citra Dewi", "Dewi Lestari", "Eko Prasetyo"}
	for _, n := range names {
		in := adult()
		in.FullName = n
		if _, err := e.svc.Create(ctx, who(facA, "doctor"), in); err != nil {
			t.Fatal(err)
		}
	}
	p := who(facA, "doctor")

	// Walk all pages of 2: newest first, no duplicates, no gaps.
	var got []string
	var cursor *domain.Cursor
	pages := 0
	for {
		page, err := e.svc.List(ctx, p, "", cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, pt := range page.Patients {
			got = append(got, pt.FullName)
		}
		if page.Next == nil {
			break
		}
		cursor = page.Next
	}
	want := []string{"Eko Prasetyo", "Dewi Lestari", "Citra Dewi", "Budi Santoso", "Andi Wijaya"}
	if pages != 3 || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("pages=%d got %v, want %v", pages, got, want)
	}

	// Exactly one full page must not advertise a next page.
	page, _ := e.svc.List(ctx, p, "", nil, 5)
	if page.Next != nil || len(page.Patients) != 5 {
		t.Fatalf("limit == total: next=%v n=%d", page.Next, len(page.Patients))
	}

	// Search by name (case-insensitive substring) and by MRN prefix.
	page, _ = e.svc.List(ctx, p, "dewi", nil, 10)
	if len(page.Patients) != 2 {
		t.Fatalf("search 'dewi': want 2, got %d", len(page.Patients))
	}
	page, _ = e.svc.List(ctx, p, "RM-000003", nil, 10)
	if len(page.Patients) != 1 || page.Patients[0].FullName != "Citra Dewi" {
		t.Fatalf("search MRN: %+v", page.Patients)
	}
}

// spyStore records the filter the service hands to the store.
type spyStore struct {
	*patientstest.MemStore
	got domain.ListFilter
}

func (s *spyStore) List(ctx context.Context, facilityID string, f domain.ListFilter) ([]domain.Patient, error) {
	s.got = f
	return s.MemStore.List(ctx, facilityID, f)
}

func TestList_SearchTextIsTrimmedAndCapped(t *testing.T) {
	spy := &spyStore{MemStore: patientstest.NewMemStore()}
	svc := service.New(spy, &audit.Memory{})
	ctx, p := context.Background(), who(facA, "doctor")

	if _, err := svc.List(ctx, p, "   siti   ", nil, 10); err != nil || spy.got.Query != "siti" {
		t.Fatalf("query must be trimmed: %v %q", err, spy.got.Query)
	}
	long := strings.Repeat("é", 5000) // multi-byte runes: must be cut on rune boundaries, not bytes
	if _, err := svc.List(ctx, p, long, nil, 10); err != nil {
		t.Fatal(err)
	}
	if n := utf8.RuneCountInString(spy.got.Query); n != 100 || !utf8.ValidString(spy.got.Query) {
		t.Fatalf("query must be capped at 100 valid runes, got %d (valid=%v)", n, utf8.ValidString(spy.got.Query))
	}
}

func TestList_LimitIsClamped(t *testing.T) {
	e, ctx := newEnv(), context.Background()
	for i := range 3 {
		in := adult()
		in.FullName = fmt.Sprintf("Pasien %d", i)
		_, _ = e.svc.Create(ctx, who(facA, "doctor"), in)
	}
	page, err := e.svc.List(ctx, who(facA, "doctor"), "", nil, 100000)
	if err != nil || len(page.Patients) != 3 {
		t.Fatalf("huge limit: %v %d", err, len(page.Patients))
	}
	page, _ = e.svc.List(ctx, who(facA, "doctor"), "", nil, 0)
	if len(page.Patients) != 3 {
		t.Fatalf("default limit: %d", len(page.Patients))
	}
}

func TestAudit(t *testing.T) {
	e, ctx := newEnv(), context.Background()
	p, _ := e.svc.Create(ctx, who(facA, "admin"), adult())
	_, _ = e.svc.Get(ctx, who(facA, "admin"), p.ID)
	_, _ = e.svc.Update(ctx, who(facA, "admin"), p.ID, adult().Input)
	_, _ = e.svc.List(ctx, who(facA, "admin"), "siti", nil, 10)
	_ = e.svc.Archive(ctx, who(facA, "admin"), p.ID)

	want := []string{"patient.create", "patient.read", "patient.update", "patient.list", "patient.archive"}
	if fmt.Sprint(e.audit.Actions()) != fmt.Sprint(want) {
		t.Fatalf("audit = %v, want %v", e.audit.Actions(), want)
	}
	for _, en := range e.audit.Entries {
		if en.FacilityID != facA || en.ActorID != "user-1" || en.Resource != "patient" {
			t.Errorf("bad audit entry: %+v", en)
		}
	}
}

func TestAudit_FailClosed(t *testing.T) {
	e, ctx := newEnv(), context.Background()
	p, _ := e.svc.Create(ctx, who(facA, "admin"), adult())
	e.audit.Err = errors.New("audit db down")

	// No audit, no data: reads must fail rather than return patient data unrecorded.
	if got, err := e.svc.Get(ctx, who(facA, "admin"), p.ID); err == nil {
		t.Fatalf("Get must fail when audit fails, returned %+v", got)
	}
	if page, err := e.svc.List(ctx, who(facA, "admin"), "", nil, 10); err == nil {
		t.Fatalf("List must fail when audit fails, returned %+v", page)
	}
}
