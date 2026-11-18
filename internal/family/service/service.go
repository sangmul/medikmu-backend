// Package service implements the family use cases. Everything is scoped to the signed-in account.
package service

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sangmul/medikmu-backend/internal/family/domain"
	"github.com/sangmul/medikmu-backend/internal/platform/authctx"
)

var (
	uuidRe     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	nikFormat  = regexp.MustCompile(`[\s-]`)
	digitsOnly = regexp.MustCompile(`^\d+$`)
	wib        = time.FixedZone("WIB", 7*60*60)
)

// Input is the writable part of a family member.
type Input struct {
	FullName       string
	Relation       string
	Sex            string
	BirthDate      string // YYYY-MM-DD
	NIK            string
	AvatarTone     string // optional, defaults to purple
	BPJSNumber     string
	BPJSHolderName string
	BPJSClass      int
}

// Service holds the family use cases.
type Service struct {
	store domain.Store
	now   func() time.Time
}

// New builds a Service.
func New(store domain.Store) *Service { return &Service{store: store, now: time.Now} }

func (s *Service) List(ctx context.Context, who authctx.Principal) ([]domain.Member, error) {
	if who.UserID == "" {
		return nil, domain.ErrNotFound
	}
	return s.store.List(ctx, who.UserID)
}

func (s *Service) Get(ctx context.Context, who authctx.Principal, id string) (domain.Member, error) {
	if who.UserID == "" || !uuidRe.MatchString(id) {
		return domain.Member{}, domain.ErrNotFound
	}
	return s.store.Get(ctx, who.UserID, id)
}

func (s *Service) Create(ctx context.Context, who authctx.Principal, in Input) (domain.Member, error) {
	if who.UserID == "" {
		return domain.Member{}, domain.ErrNotFound
	}
	m, err := s.build(in)
	if err != nil {
		return domain.Member{}, err
	}
	return s.store.Create(ctx, who.UserID, m)
}

func (s *Service) Update(ctx context.Context, who authctx.Principal, id string, in Input) (domain.Member, error) {
	if who.UserID == "" || !uuidRe.MatchString(id) {
		return domain.Member{}, domain.ErrNotFound
	}
	m, err := s.build(in)
	if err != nil {
		return domain.Member{}, err
	}
	m.ID = id
	return s.store.Update(ctx, who.UserID, m)
}

// SetNIK saves only the NIK (the mobile app asks for it separately, once SATUSEHAT needs it).
func (s *Service) SetNIK(ctx context.Context, who authctx.Principal, id, nik string) (domain.Member, error) {
	if who.UserID == "" || !uuidRe.MatchString(id) {
		return domain.Member{}, domain.ErrNotFound
	}
	n, msg := normalizeNIK(nik, true)
	if msg != "" {
		return domain.Member{}, &domain.ValidationError{Fields: map[string]string{"nik": msg}}
	}
	return s.store.SetNIK(ctx, who.UserID, id, n)
}

func (s *Service) Delete(ctx context.Context, who authctx.Principal, id string) error {
	if who.UserID == "" || !uuidRe.MatchString(id) {
		return domain.ErrNotFound
	}
	return s.store.Delete(ctx, who.UserID, id)
}

func (s *Service) build(in Input) (domain.Member, error) {
	fields := map[string]string{}
	m := domain.Member{BPJSHolderName: strings.TrimSpace(in.BPJSHolderName), AvatarTone: in.AvatarTone}

	m.FullName = strings.TrimSpace(in.FullName)
	if n := utf8.RuneCountInString(m.FullName); n < 2 || n > 120 {
		fields["full_name"] = "nama harus 2-120 karakter"
	}
	switch in.Relation {
	case domain.RelationSelf, domain.RelationSpouse, domain.RelationChild, domain.RelationParent, domain.RelationSibling, domain.RelationOther:
		m.Relation = in.Relation
	default:
		fields["relation"] = "pilih self, spouse, child, parent, sibling, atau other"
	}
	switch in.Sex {
	case "F", "M":
		m.Sex = in.Sex
	default:
		fields["sex"] = "pilih F atau M"
	}

	today := s.now().In(wib)
	dob, err := time.ParseInLocation("2006-01-02", in.BirthDate, time.UTC)
	switch {
	case err != nil:
		fields["birth_date"] = "format tanggal harus YYYY-MM-DD"
	case dob.After(time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)):
		fields["birth_date"] = "tanggal lahir tidak boleh di masa depan"
	case dob.Year() < 1900:
		fields["birth_date"] = "tanggal lahir tidak valid"
	default:
		m.BirthDate = dob
	}

	if nik, msg := normalizeNIK(in.NIK, false); msg != "" {
		fields["nik"] = msg
	} else {
		m.NIK = nik
	}

	switch in.AvatarTone {
	case "":
		m.AvatarTone = "purple"
	case "purple", "blue", "orange", "teal":
	default:
		fields["avatar_tone"] = "pilih purple, blue, orange, atau teal"
	}

	if num := nikFormat.ReplaceAllString(strings.TrimSpace(in.BPJSNumber), ""); num != "" {
		if !digitsOnly.MatchString(num) || len(num) != 13 {
			fields["bpjs_number"] = "nomor BPJS harus 13 digit angka"
		} else {
			m.BPJSNumber = num
		}
	}
	if m.BPJSNumber == "" && (m.BPJSHolderName != "" || in.BPJSClass != 0) {
		fields["bpjs_number"] = "isi nomor BPJS untuk menyimpan data kartu"
	}
	if in.BPJSClass < 0 || in.BPJSClass > 3 {
		fields["bpjs_class"] = "kelas BPJS 1, 2, atau 3"
	} else {
		m.BPJSClass = in.BPJSClass
	}
	if utf8.RuneCountInString(m.BPJSHolderName) > 120 {
		fields["bpjs_holder_name"] = "maksimal 120 karakter"
	}

	if len(fields) > 0 {
		return domain.Member{}, &domain.ValidationError{Fields: fields}
	}
	return m, nil
}

// normalizeNIK drops spaces and dashes and checks for 16 digits. An empty NIK is fine unless required.
func normalizeNIK(in string, required bool) (nik, msg string) {
	nik = nikFormat.ReplaceAllString(strings.TrimSpace(in), "")
	switch {
	case nik == "" && required:
		return "", "isi 16 digit NIK"
	case nik == "":
		return "", ""
	case !digitsOnly.MatchString(nik):
		return "", "NIK hanya boleh angka"
	case len(nik) != 16:
		return "", "NIK harus 16 digit"
	}
	return nik, ""
}
