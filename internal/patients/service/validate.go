package service

import (
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sangmul/medikmu-backend/internal/patients/domain"
)

// wib is Indonesia's western time zone (UTC+7), used to decide what "today" and "age" mean.
var wib = time.FixedZone("WIB", 7*60*60)

// Input is the writable part of a patient, as supplied by clients.
type Input struct {
	FullName         string
	Sex              string
	BirthDate        string // YYYY-MM-DD
	BirthPlace       string
	NIK              string
	GuardianName     string
	GuardianRelation string
	Phone            string
	Email            string
	Address          string
	PaymentType      string
	BPJSNumber       string
	Allergies        []string
	Conditions       []string
	Notes            string
}

var (
	nonDigits  = regexp.MustCompile(`\D`)
	nikFormat  = regexp.MustCompile(`[\s-]`)
	digitsOnly = regexp.MustCompile(`^\d+$`)
)

// build validates in and returns a normalized patient (without ID, MRN or timestamps).
func build(in Input, now time.Time) (domain.Patient, error) {
	fields := map[string]string{}
	p := domain.Patient{
		GuardianRelation: clip(in.GuardianRelation, 40),
		BirthPlace:       clip(in.BirthPlace, 80),
		Address:          strings.TrimSpace(in.Address),
		Notes:            strings.TrimSpace(in.Notes),
	}

	p.FullName = strings.TrimSpace(in.FullName)
	if n := utf8.RuneCountInString(p.FullName); n < 3 || n > 120 {
		fields["full_name"] = "nama harus 3-120 karakter"
	}

	switch in.Sex {
	case domain.SexFemale, domain.SexMale:
		p.Sex = in.Sex
	default:
		fields["sex"] = "pilih F atau M"
	}

	today := now.In(wib)
	dob, err := time.ParseInLocation("2006-01-02", in.BirthDate, time.UTC)
	switch {
	case err != nil:
		fields["birth_date"] = "format tanggal harus YYYY-MM-DD"
	case dob.After(time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)):
		fields["birth_date"] = "tanggal lahir tidak boleh di masa depan"
	case dob.Year() < 1900:
		fields["birth_date"] = "tanggal lahir tidak valid"
	default:
		p.BirthDate = dob
	}

	if nik := nikFormat.ReplaceAllString(strings.TrimSpace(in.NIK), ""); nik != "" {
		if !digitsOnly.MatchString(nik) || len(nik) != 16 {
			fields["nik"] = "NIK harus 16 digit angka"
		} else {
			p.NIK = nik
		}
	}

	p.GuardianName = strings.TrimSpace(in.GuardianName)
	if utf8.RuneCountInString(p.GuardianName) > 120 {
		fields["guardian_name"] = "maksimal 120 karakter"
	} else if _, ok := fields["birth_date"]; !ok && age(p.BirthDate, today) < 18 && utf8.RuneCountInString(p.GuardianName) < 3 {
		fields["guardian_name"] = "nama orang tua/wali wajib untuk pasien di bawah 18 tahun"
	}

	phone := strings.TrimSpace(in.Phone)
	digits := nonDigits.ReplaceAllString(phone, "")
	if len(digits) < 9 || len(digits) > 15 {
		fields["phone"] = "nomor telepon harus 9-15 digit"
	} else {
		p.Phone = digits
		if strings.HasPrefix(phone, "+") {
			p.Phone = "+" + digits
		}
	}

	if email := strings.TrimSpace(in.Email); email != "" {
		if _, err := mail.ParseAddress(email); err != nil || strings.ContainsAny(email, " <>") {
			fields["email"] = "format email tidak valid"
		} else {
			p.Email = strings.ToLower(email)
		}
	}

	switch in.PaymentType {
	case domain.PaymentBPJS, domain.PaymentPrivate, domain.PaymentInsurance:
		p.PaymentType = in.PaymentType
	default:
		fields["payment_type"] = "pilih bpjs, private, atau insurance"
	}
	if bpjs := nonDigits.ReplaceAllString(in.BPJSNumber, ""); strings.TrimSpace(in.BPJSNumber) != "" {
		switch {
		case in.PaymentType != domain.PaymentBPJS:
			fields["bpjs_number"] = "nomor BPJS hanya untuk pembayaran bpjs"
		case len(bpjs) != 13:
			fields["bpjs_number"] = "nomor BPJS harus 13 digit"
		default:
			p.BPJSNumber = bpjs
		}
	}

	var ok bool
	if p.Allergies, ok = cleanList(in.Allergies); !ok {
		fields["allergies"] = "maksimal 20 item, masing-masing maksimal 60 karakter"
	}
	if p.Conditions, ok = cleanList(in.Conditions); !ok {
		fields["conditions"] = "maksimal 20 item, masing-masing maksimal 60 karakter"
	}
	if utf8.RuneCountInString(p.Address) > 300 {
		fields["address"] = "maksimal 300 karakter"
	}
	if utf8.RuneCountInString(p.Notes) > 2000 {
		fields["notes"] = "maksimal 2000 karakter"
	}

	if len(fields) > 0 {
		return domain.Patient{}, &domain.ValidationError{Fields: fields}
	}
	return p, nil
}

// age returns completed years between birth and today (both calendar dates).
func age(birth, today time.Time) int {
	y := today.Year() - birth.Year()
	if today.Month() < birth.Month() || (today.Month() == birth.Month() && today.Day() < birth.Day()) {
		y--
	}
	return y
}

// cleanList trims, drops blanks and duplicates (case-insensitive), and enforces size limits.
func cleanList(in []string) ([]string, bool) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[strings.ToLower(s)] {
			continue
		}
		if utf8.RuneCountInString(s) > 60 {
			return nil, false
		}
		seen[strings.ToLower(s)] = true
		out = append(out, s)
	}
	return out, len(out) <= 20
}

func clip(s string, max int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		return string(r[:max])
	}
	return s
}
