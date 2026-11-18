package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/sangmul/medikmu-backend/internal/family/domain"
	"github.com/sangmul/medikmu-backend/internal/family/familytest"
	"github.com/sangmul/medikmu-backend/internal/family/service"
	"github.com/sangmul/medikmu-backend/internal/platform/authctx"
)

func newSvc() *service.Service { return service.New(familytest.NewMemStore()) }

var (
	ana  = authctx.Principal{UserID: "user-ana"}
	budi = authctx.Principal{UserID: "user-budi"}
)

func child() service.Input {
	return service.Input{FullName: "Aira Putri", Relation: "child", Sex: "F", BirthDate: "2023-05-10"}
}

func TestCreate_DefaultsAndNormalization(t *testing.T) {
	svc, ctx := newSvc(), context.Background()
	in := child()
	in.NIK = "3174 0123-4567 8901"
	in.BPJSNumber, in.BPJSHolderName, in.BPJSClass = "0001-2345-67890", "AIRA PUTRI", 2

	m, err := svc.Create(ctx, ana, in)
	if err != nil {
		t.Fatal(err)
	}
	if m.NIK != "3174012345678901" || m.BPJSNumber != "0001234567890" || m.AvatarTone != "purple" || m.BPJSClass != 2 || m.UserID != "user-ana" {
		t.Fatalf("not normalized: %+v", m)
	}
}

func TestCreate_Validation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*service.Input)
		field  string
	}{
		{"short name", func(i *service.Input) { i.FullName = "A" }, "full_name"},
		{"bad relation", func(i *service.Input) { i.Relation = "neighbour" }, "relation"},
		{"bad sex", func(i *service.Input) { i.Sex = "X" }, "sex"},
		{"bad date", func(i *service.Input) { i.BirthDate = "10-05-2023" }, "birth_date"},
		{"future date", func(i *service.Input) { i.BirthDate = "2999-01-01" }, "birth_date"},
		{"short nik", func(i *service.Input) { i.NIK = "123" }, "nik"},
		{"alpha nik", func(i *service.Input) { i.NIK = "31740123456789ab" }, "nik"},
		{"bad tone", func(i *service.Input) { i.AvatarTone = "neon" }, "avatar_tone"},
		{"short bpjs", func(i *service.Input) { i.BPJSNumber = "12345" }, "bpjs_number"},
		{"bpjs class out of range", func(i *service.Input) { i.BPJSNumber = "0001234567890"; i.BPJSClass = 4 }, "bpjs_class"},
		{"card details without number", func(i *service.Input) { i.BPJSHolderName = "AIRA" }, "bpjs_number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := child()
			tt.mutate(&in)
			_, err := newSvc().Create(context.Background(), ana, in)
			var ve *domain.ValidationError
			if !errors.As(err, &ve) || ve.Fields[tt.field] == "" {
				t.Fatalf("want validation error on %q, got %v", tt.field, err)
			}
		})
	}
}

func TestOwnershipIsolation(t *testing.T) {
	svc, ctx := newSvc(), context.Background()
	m, _ := svc.Create(ctx, ana, child())

	if _, err := svc.Get(ctx, budi, m.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get: want ErrNotFound, got %v", err)
	}
	if _, err := svc.Update(ctx, budi, m.ID, child()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("update: want ErrNotFound, got %v", err)
	}
	if _, err := svc.SetNIK(ctx, budi, m.ID, "3174012345678901"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("set nik: want ErrNotFound, got %v", err)
	}
	if err := svc.Delete(ctx, budi, m.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("delete: want ErrNotFound, got %v", err)
	}
	if list, _ := svc.List(ctx, budi); len(list) != 0 {
		t.Errorf("budi sees ana's family: %v", list)
	}
	if got, err := svc.Get(ctx, ana, m.ID); err != nil || got.FullName != "Aira Putri" {
		t.Fatalf("owner lost the member: %v %+v", err, got)
	}
}

func TestAnonymousAndBadIDs(t *testing.T) {
	svc, ctx := newSvc(), context.Background()
	if _, err := svc.List(ctx, authctx.Principal{}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("anonymous list: %v", err)
	}
	if _, err := svc.Create(ctx, authctx.Principal{}, child()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("anonymous create: %v", err)
	}
	for _, id := range []string{"", "abc", "1; DROP TABLE x", "../x"} {
		if _, err := svc.Get(ctx, ana, id); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("id %q: want ErrNotFound, got %v", id, err)
		}
	}
}

func TestSetNIK(t *testing.T) {
	svc, ctx := newSvc(), context.Background()
	a, _ := svc.Create(ctx, ana, child())
	b2 := child()
	b2.FullName = "Raka Putra"
	b, _ := svc.Create(ctx, ana, b2)

	got, err := svc.SetNIK(ctx, ana, a.ID, "3174 0123 4567 8901")
	if err != nil || got.NIK != "3174012345678901" {
		t.Fatalf("set: %v %+v", err, got)
	}
	var ve *domain.ValidationError
	for _, bad := range []string{"", "123", "abcdefghijklmnop"} {
		if _, err := svc.SetNIK(ctx, ana, a.ID, bad); !errors.As(err, &ve) {
			t.Errorf("nik %q: want ValidationError, got %v", bad, err)
		}
	}
	// One NIK per person within an account; another account may use the same number.
	if _, err := svc.SetNIK(ctx, ana, b.ID, "3174012345678901"); !errors.Is(err, domain.ErrNIKTaken) {
		t.Fatalf("duplicate nik: want ErrNIKTaken, got %v", err)
	}
	other, _ := svc.Create(ctx, budi, child())
	if _, err := svc.SetNIK(ctx, budi, other.ID, "3174012345678901"); err != nil {
		t.Fatalf("same NIK on another account: %v", err)
	}
}

func TestUpdateReplacesFields(t *testing.T) {
	svc, ctx := newSvc(), context.Background()
	m, _ := svc.Create(ctx, ana, child())
	in := child()
	in.FullName, in.AvatarTone = "Aira Putri Baru", "teal"
	got, err := svc.Update(ctx, ana, m.ID, in)
	if err != nil || got.FullName != "Aira Putri Baru" || got.AvatarTone != "teal" || got.ID != m.ID || got.UserID != "user-ana" {
		t.Fatalf("update: %v %+v", err, got)
	}
	if err := svc.Delete(ctx, ana, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, ana, m.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestCap(t *testing.T) {
	svc, ctx := newSvc(), context.Background()
	for i := range domain.MaxMembersPerUser {
		in := child()
		in.FullName = fmt.Sprintf("Anak %d", i)
		if _, err := svc.Create(ctx, ana, in); err != nil {
			t.Fatalf("member %d: %v", i, err)
		}
	}
	if _, err := svc.Create(ctx, ana, child()); !errors.Is(err, domain.ErrLimitReached) {
		t.Fatalf("over cap: want ErrLimitReached, got %v", err)
	}
	if _, err := svc.Create(ctx, budi, child()); err != nil {
		t.Fatalf("cap is per account: %v", err)
	}
}
