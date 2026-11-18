// Package familyhttp exposes the patient's family members over HTTP (mobile app).
package familyhttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/sangmul/medikmu-backend/internal/family/domain"
	"github.com/sangmul/medikmu-backend/internal/family/service"
	"github.com/sangmul/medikmu-backend/internal/platform/authctx"
	"github.com/sangmul/medikmu-backend/internal/platform/httpx"
)

// Family is the subset of the service the HTTP layer needs.
type Family interface {
	List(ctx context.Context, who authctx.Principal) ([]domain.Member, error)
	Get(ctx context.Context, who authctx.Principal, id string) (domain.Member, error)
	Create(ctx context.Context, who authctx.Principal, in service.Input) (domain.Member, error)
	Update(ctx context.Context, who authctx.Principal, id string, in service.Input) (domain.Member, error)
	SetNIK(ctx context.Context, who authctx.Principal, id, nik string) (domain.Member, error)
	Delete(ctx context.Context, who authctx.Principal, id string) error
}

// Handler serves the family endpoints.
type Handler struct {
	svc    Family
	verify httpx.TokenVerifier
}

// New returns a Handler. verify authenticates access tokens.
func New(svc Family, verify httpx.TokenVerifier) *Handler { return &Handler{svc: svc, verify: verify} }

// Mount registers the routes. They are scoped to the signed-in account; no facility is involved.
func (h *Handler) Mount(mux *http.ServeMux) {
	auth := httpx.RequireAuth(h.verify)
	route := func(pattern string, f http.HandlerFunc) { mux.Handle(pattern, auth(f)) }
	route("GET /api/v1/family-members", h.list)
	route("POST /api/v1/family-members", h.create)
	route("GET /api/v1/family-members/{id}", h.get)
	route("PUT /api/v1/family-members/{id}", h.update)
	route("PUT /api/v1/family-members/{id}/nik", h.setNIK)
	route("DELETE /api/v1/family-members/{id}", h.delete)
}

// --- bodies (mirror api/openapi.yaml) ---

type bpjsBody struct {
	Number       string `json:"number"`
	HolderName   string `json:"holder_name"`
	ServiceClass int    `json:"service_class"`
}

type memberRequest struct {
	FullName   string    `json:"full_name"`
	Relation   string    `json:"relation"`
	Sex        string    `json:"sex"`
	BirthDate  string    `json:"birth_date"`
	NIK        string    `json:"nik"`
	AvatarTone string    `json:"avatar_tone"`
	BPJS       *bpjsBody `json:"bpjs"`
}

type memberResponse struct {
	ID         string    `json:"id"`
	FullName   string    `json:"full_name"`
	Relation   string    `json:"relation"`
	Sex        string    `json:"sex"`
	BirthDate  string    `json:"birth_date"`
	NIK        string    `json:"nik,omitempty"`
	NIKMasked  string    `json:"nik_masked,omitempty"`
	HasNIK     bool      `json:"has_nik"`
	AvatarTone string    `json:"avatar_tone"`
	BPJS       *bpjsBody `json:"bpjs"`
	CreatedAt  string    `json:"created_at"`
	UpdatedAt  string    `json:"updated_at"`
}

type nikRequest struct {
	NIK string `json:"nik"`
}

func (r memberRequest) input() service.Input {
	in := service.Input{FullName: r.FullName, Relation: r.Relation, Sex: r.Sex, BirthDate: r.BirthDate, NIK: r.NIK, AvatarTone: r.AvatarTone}
	if r.BPJS != nil {
		in.BPJSNumber, in.BPJSHolderName, in.BPJSClass = r.BPJS.Number, r.BPJS.HolderName, r.BPJS.ServiceClass
	}
	return in
}

func toResponse(m domain.Member) memberResponse {
	resp := memberResponse{
		ID: m.ID, FullName: m.FullName, Relation: m.Relation, Sex: m.Sex,
		BirthDate: m.BirthDate.Format("2006-01-02"), NIK: m.NIK, HasNIK: m.NIK != "", AvatarTone: m.AvatarTone,
		CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: m.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if len(m.NIK) == 16 {
		resp.NIKMasked = m.NIK[:4] + " **** **** " + m.NIK[12:]
	}
	if m.BPJSNumber != "" {
		resp.BPJS = &bpjsBody{Number: m.BPJSNumber, HolderName: m.BPJSHolderName, ServiceClass: m.BPJSClass}
	}
	return resp
}

// --- handlers ---

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	members, err := h.svc.List(r.Context(), who)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]memberResponse, 0, len(members))
	for _, m := range members {
		out = append(out, toResponse(m))
	}
	write(w, http.StatusOK, map[string]any{"data": out})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	var req memberRequest
	if !decode(w, r, &req) {
		return
	}
	m, err := h.svc.Create(r.Context(), who, req.input())
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/family-members/"+m.ID)
	write(w, http.StatusCreated, toResponse(m))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	m, err := h.svc.Get(r.Context(), who, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	write(w, http.StatusOK, toResponse(m))
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	var req memberRequest
	if !decode(w, r, &req) {
		return
	}
	m, err := h.svc.Update(r.Context(), who, r.PathValue("id"), req.input())
	if err != nil {
		writeError(w, r, err)
		return
	}
	write(w, http.StatusOK, toResponse(m))
}

func (h *Handler) setNIK(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	var req nikRequest
	if !decode(w, r, &req) {
		return
	}
	m, err := h.svc.SetNIK(r.Context(), who, r.PathValue("id"), req.NIK)
	if err != nil {
		writeError(w, r, err)
		return
	}
	write(w, http.StatusOK, toResponse(m))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	if err := h.svc.Delete(r.Context(), who, r.PathValue("id")); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := httpx.DecodeJSON(w, r, dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Permintaan tidak valid.")
		return false
	}
	return true
}

// write sends personal data (NIK, BPJS): never cache it.
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, status, v)
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *domain.ValidationError
	switch {
	case errors.As(err, &ve):
		httpx.ErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed", "Data tidak valid.", ve.Fields)
	case errors.Is(err, domain.ErrNIKTaken):
		httpx.Error(w, http.StatusConflict, "nik_taken", "NIK ini sudah ada di daftar keluarga Anda.")
	case errors.Is(err, domain.ErrLimitReached):
		httpx.Error(w, http.StatusConflict, "family_limit", "Jumlah anggota keluarga sudah mencapai batas.")
	case errors.Is(err, domain.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "Anggota keluarga tidak ditemukan.")
	default:
		slog.Error("family request failed", "error", err, "path", r.URL.Path)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan pada server.")
	}
}
