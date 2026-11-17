// Package patientshttp exposes the patients module over HTTP.
package patientshttp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sangmul/medikmu-backend/internal/patients/domain"
	"github.com/sangmul/medikmu-backend/internal/patients/service"
	"github.com/sangmul/medikmu-backend/internal/platform/authctx"
	"github.com/sangmul/medikmu-backend/internal/platform/httpx"
)

// Patients is the subset of the patients service the HTTP layer needs.
type Patients interface {
	Create(ctx context.Context, who authctx.Principal, in service.CreateInput) (domain.Patient, error)
	Get(ctx context.Context, who authctx.Principal, id string) (domain.Patient, error)
	Update(ctx context.Context, who authctx.Principal, id string, in service.Input) (domain.Patient, error)
	Archive(ctx context.Context, who authctx.Principal, id string) error
	List(ctx context.Context, who authctx.Principal, q string, cursor *domain.Cursor, limit int) (domain.Page, error)
}

// Handler serves the patients endpoints.
type Handler struct {
	svc    Patients
	verify httpx.TokenVerifier
}

// New returns a Handler. verify authenticates access tokens (the auth service's VerifyAccessToken).
func New(svc Patients, verify httpx.TokenVerifier) *Handler {
	return &Handler{svc: svc, verify: verify}
}

// Mount registers the patients routes. Role and facility checks live in the service.
func (h *Handler) Mount(mux *http.ServeMux) {
	auth := httpx.RequireAuth(h.verify)
	mux.Handle("GET /api/v1/patients", auth(http.HandlerFunc(h.list)))
	mux.Handle("POST /api/v1/patients", auth(http.HandlerFunc(h.create)))
	mux.Handle("GET /api/v1/patients/{id}", auth(http.HandlerFunc(h.get)))
	mux.Handle("PUT /api/v1/patients/{id}", auth(http.HandlerFunc(h.update)))
	mux.Handle("DELETE /api/v1/patients/{id}", auth(http.HandlerFunc(h.archive)))
}

// --- bodies (mirror api/openapi.yaml) ---

type patientFields struct {
	FullName         string   `json:"full_name"`
	Sex              string   `json:"sex"`
	BirthDate        string   `json:"birth_date"`
	BirthPlace       string   `json:"birth_place"`
	NIK              string   `json:"nik"`
	GuardianName     string   `json:"guardian_name"`
	GuardianRelation string   `json:"guardian_relation"`
	Phone            string   `json:"phone"`
	Email            string   `json:"email"`
	Address          string   `json:"address"`
	PaymentType      string   `json:"payment_type"`
	BPJSNumber       string   `json:"bpjs_number"`
	Allergies        []string `json:"allergies"`
	Conditions       []string `json:"conditions"`
	Notes            string   `json:"notes"`
}

type createRequest struct {
	patientFields
	Consent bool `json:"consent"`
}

func (f patientFields) input() service.Input {
	return service.Input{
		FullName: f.FullName, Sex: f.Sex, BirthDate: f.BirthDate, BirthPlace: f.BirthPlace, NIK: f.NIK,
		GuardianName: f.GuardianName, GuardianRelation: f.GuardianRelation, Phone: f.Phone, Email: f.Email,
		Address: f.Address, PaymentType: f.PaymentType, BPJSNumber: f.BPJSNumber,
		Allergies: f.Allergies, Conditions: f.Conditions, Notes: f.Notes,
	}
}

type patientResponse struct {
	ID        string `json:"id"`
	MRN       string `json:"mrn"`
	ConsentAt string `json:"consent_at"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	patientFields
}

type summaryResponse struct {
	ID           string   `json:"id"`
	MRN          string   `json:"mrn"`
	FullName     string   `json:"full_name"`
	Sex          string   `json:"sex"`
	BirthDate    string   `json:"birth_date"`
	NIKMasked    string   `json:"nik_masked,omitempty"`
	GuardianName string   `json:"guardian_name,omitempty"`
	Phone        string   `json:"phone"`
	PaymentType  string   `json:"payment_type"`
	Allergies    []string `json:"allergies"`
}

type pageResponse struct {
	Data       []summaryResponse `json:"data"`
	NextCursor *string           `json:"next_cursor"`
}

func toResponse(p domain.Patient) patientResponse {
	return patientResponse{
		ID: p.ID, MRN: p.MRN,
		ConsentAt: p.ConsentAt.UTC().Format(time.RFC3339),
		CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: p.UpdatedAt.UTC().Format(time.RFC3339),
		patientFields: patientFields{
			FullName: p.FullName, Sex: p.Sex, BirthDate: p.BirthDate.Format("2006-01-02"),
			BirthPlace: p.BirthPlace, NIK: p.NIK, GuardianName: p.GuardianName,
			GuardianRelation: p.GuardianRelation, Phone: p.Phone, Email: p.Email, Address: p.Address,
			PaymentType: p.PaymentType, BPJSNumber: p.BPJSNumber,
			Allergies: nonNil(p.Allergies), Conditions: nonNil(p.Conditions), Notes: p.Notes,
		},
	}
}

func toSummary(p domain.Patient) summaryResponse {
	return summaryResponse{
		ID: p.ID, MRN: p.MRN, FullName: p.FullName, Sex: p.Sex,
		BirthDate: p.BirthDate.Format("2006-01-02"), NIKMasked: maskNIK(p.NIK),
		GuardianName: p.GuardianName, Phone: p.Phone, PaymentType: p.PaymentType,
		Allergies: nonNil(p.Allergies),
	}
}

// maskNIK shows enough to recognise a NIK, not enough to read it over a shoulder: "3174 **** **** 0001".
func maskNIK(nik string) string {
	if len(nik) != 16 {
		return ""
	}
	return nik[:4] + " **** **** " + nik[12:]
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// --- handlers ---

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	q := r.URL.Query()

	limit := 0
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			httpx.Error(w, http.StatusBadRequest, "invalid_request", "Parameter limit harus 1-100.")
			return
		}
		limit = n
	}
	var cursor *domain.Cursor
	if v := q.Get("cursor"); v != "" {
		c, err := decodeCursor(v)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_request", "Parameter cursor tidak valid.")
			return
		}
		cursor = &c
	}

	page, err := h.svc.List(r.Context(), who, q.Get("q"), cursor, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp := pageResponse{Data: make([]summaryResponse, 0, len(page.Patients))}
	for _, p := range page.Patients {
		resp.Data = append(resp.Data, toSummary(p))
	}
	if page.Next != nil {
		c := encodeCursor(*page.Next)
		resp.NextCursor = &c
	}
	writePHI(w, http.StatusOK, resp)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	var req createRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Permintaan tidak valid.")
		return
	}
	p, err := h.svc.Create(r.Context(), who, service.CreateInput{Input: req.input(), Consent: req.Consent})
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/patients/"+p.ID)
	writePHI(w, http.StatusCreated, toResponse(p))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	p, err := h.svc.Get(r.Context(), who, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writePHI(w, http.StatusOK, toResponse(p))
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	var req patientFields
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Permintaan tidak valid.")
		return
	}
	p, err := h.svc.Update(r.Context(), who, r.PathValue("id"), req.input())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writePHI(w, http.StatusOK, toResponse(p))
}

func (h *Handler) archive(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	if err := h.svc.Archive(r.Context(), who, r.PathValue("id")); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

// writePHI keeps patient data out of shared and browser caches.
func writePHI(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, status, v)
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *domain.ValidationError
	switch {
	case errors.As(err, &ve):
		httpx.ErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed", "Data tidak valid.", ve.Fields)
	case errors.Is(err, domain.ErrNIKTaken):
		httpx.Error(w, http.StatusConflict, "nik_taken", "NIK sudah terdaftar di fasilitas ini.")
	case errors.Is(err, domain.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "patient_not_found", "Pasien tidak ditemukan.")
	case errors.Is(err, domain.ErrForbidden):
		httpx.Error(w, http.StatusForbidden, "forbidden", "Anda tidak memiliki akses.")
	default:
		slog.Error("patients request failed", "error", err, "path", r.URL.Path)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan pada server.")
	}
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// The cursor is opaque to clients: base64url("<unix nanos>|<id>").
func encodeCursor(c domain.Cursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d|%s", c.CreatedAt.UnixNano(), c.ID)))
}

func decodeCursor(s string) (domain.Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return domain.Cursor{}, err
	}
	nanos, id, ok := strings.Cut(string(raw), "|")
	n, perr := strconv.ParseInt(nanos, 10, 64)
	if !ok || perr != nil || !uuidRe.MatchString(id) {
		return domain.Cursor{}, errors.New("malformed cursor")
	}
	return domain.Cursor{CreatedAt: time.Unix(0, n).UTC(), ID: id}, nil
}
