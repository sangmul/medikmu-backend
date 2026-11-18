// Package facilitieshttp exposes facilities, team and invites over HTTP.
package facilitieshttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/sangmul/medikmu-backend/internal/facilities/domain"
	"github.com/sangmul/medikmu-backend/internal/facilities/service"
	"github.com/sangmul/medikmu-backend/internal/platform/authctx"
	"github.com/sangmul/medikmu-backend/internal/platform/httpx"
)

// Facilities is the subset of the service the HTTP layer needs.
type Facilities interface {
	CreateFacility(ctx context.Context, who authctx.Principal, in service.FacilityInput) (domain.Facility, error)
	Current(ctx context.Context, who authctx.Principal) (domain.Facility, error)
	UpdateCurrent(ctx context.Context, who authctx.Principal, in service.FacilityInput) (domain.Facility, error)
	Team(ctx context.Context, who authctx.Principal) ([]domain.Member, error)
	RemoveMember(ctx context.Context, who authctx.Principal, userID string) error
	Invite(ctx context.Context, who authctx.Principal, email, role string) (service.InviteResult, error)
	Invites(ctx context.Context, who authctx.Principal) ([]domain.Invite, error)
	RevokeInvite(ctx context.Context, who authctx.Principal, id string) error
	AcceptInvite(ctx context.Context, who authctx.Principal, token string) (domain.Invite, error)
}

// Handler serves the facilities endpoints.
type Handler struct {
	svc    Facilities
	verify httpx.TokenVerifier
}

// New returns a Handler. verify authenticates access tokens.
func New(svc Facilities, verify httpx.TokenVerifier) *Handler {
	return &Handler{svc: svc, verify: verify}
}

// Mount registers the routes. Role and facility checks live in the service.
func (h *Handler) Mount(mux *http.ServeMux) {
	auth := httpx.RequireAuth(h.verify)
	route := func(pattern string, f http.HandlerFunc) { mux.Handle(pattern, auth(f)) }

	route("POST /api/v1/facilities", h.createFacility)
	route("GET /api/v1/facilities/current", h.current)
	route("PUT /api/v1/facilities/current", h.updateCurrent)
	route("GET /api/v1/team", h.team)
	route("DELETE /api/v1/team/{userId}", h.removeMember)
	route("POST /api/v1/invites", h.createInvite)
	route("GET /api/v1/invites", h.listInvites)
	route("DELETE /api/v1/invites/{id}", h.revokeInvite)
	route("POST /api/v1/invites/accept", h.acceptInvite)
}

// --- bodies (mirror api/openapi.yaml) ---

type facilityRequest struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"` // create only
	Phone   string `json:"phone"`
	Address string `json:"address"`
}

type updateRequest struct {
	Name    string `json:"name"`
	Phone   string `json:"phone"`
	Address string `json:"address"`
}

type facilityResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Phone     string `json:"phone"`
	Address   string `json:"address"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type memberResponse struct {
	UserID   string `json:"user_id"`
	FullName string `json:"full_name"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	JoinedAt string `json:"joined_at"`
}

type inviteRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type inviteResponse struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	ExpiresAt string `json:"expires_at"`
	CreatedAt string `json:"created_at"`
}

type createdInviteResponse struct {
	inviteResponse
	// Token is shown once. Share it with the invitee; only its hash is stored.
	Token string `json:"token"`
}

type acceptRequest struct {
	Token string `json:"token"`
}

type acceptResponse struct {
	FacilityID string `json:"facility_id"`
	Role       string `json:"role"`
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func toFacility(f domain.Facility) facilityResponse {
	return facilityResponse{ID: f.ID, Name: f.Name, Kind: f.Kind, Phone: f.Phone, Address: f.Address,
		CreatedAt: rfc3339(f.CreatedAt), UpdatedAt: rfc3339(f.UpdatedAt)}
}

func toInvite(i domain.Invite) inviteResponse {
	return inviteResponse{ID: i.ID, Email: i.Email, Role: i.Role, ExpiresAt: rfc3339(i.ExpiresAt), CreatedAt: rfc3339(i.CreatedAt)}
}

// --- handlers ---

func (h *Handler) createFacility(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	var req facilityRequest
	if !decode(w, r, &req) {
		return
	}
	f, err := h.svc.CreateFacility(r.Context(), who, service.FacilityInput{Name: req.Name, Kind: req.Kind, Phone: req.Phone, Address: req.Address})
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/facilities/current")
	write(w, http.StatusCreated, toFacility(f))
}

func (h *Handler) current(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	f, err := h.svc.Current(r.Context(), who)
	if err != nil {
		writeError(w, r, err)
		return
	}
	write(w, http.StatusOK, toFacility(f))
}

func (h *Handler) updateCurrent(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	var req updateRequest
	if !decode(w, r, &req) {
		return
	}
	f, err := h.svc.UpdateCurrent(r.Context(), who, service.FacilityInput{Name: req.Name, Phone: req.Phone, Address: req.Address})
	if err != nil {
		writeError(w, r, err)
		return
	}
	write(w, http.StatusOK, toFacility(f))
}

func (h *Handler) team(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	members, err := h.svc.Team(r.Context(), who)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]memberResponse, 0, len(members))
	for _, m := range members {
		out = append(out, memberResponse{UserID: m.UserID, FullName: m.FullName, Email: m.Email, Role: m.Role, JoinedAt: rfc3339(m.JoinedAt)})
	}
	write(w, http.StatusOK, map[string]any{"data": out})
}

func (h *Handler) removeMember(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	if err := h.svc.RemoveMember(r.Context(), who, r.PathValue("userId")); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) createInvite(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	var req inviteRequest
	if !decode(w, r, &req) {
		return
	}
	res, err := h.svc.Invite(r.Context(), who, req.Email, req.Role)
	if err != nil {
		writeError(w, r, err)
		return
	}
	write(w, http.StatusCreated, createdInviteResponse{inviteResponse: toInvite(res.Invite), Token: res.Token})
}

func (h *Handler) listInvites(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	invites, err := h.svc.Invites(r.Context(), who)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]inviteResponse, 0, len(invites))
	for _, i := range invites {
		out = append(out, toInvite(i))
	}
	write(w, http.StatusOK, map[string]any{"data": out})
}

func (h *Handler) revokeInvite(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	if err := h.svc.RevokeInvite(r.Context(), who, r.PathValue("id")); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) acceptInvite(w http.ResponseWriter, r *http.Request) {
	who, _ := authctx.From(r.Context())
	var req acceptRequest
	if !decode(w, r, &req) {
		return
	}
	inv, err := h.svc.AcceptInvite(r.Context(), who, req.Token)
	if err != nil {
		writeError(w, r, err)
		return
	}
	write(w, http.StatusOK, acceptResponse{FacilityID: inv.FacilityID, Role: inv.Role})
}

// --- helpers ---

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := httpx.DecodeJSON(w, r, dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Permintaan tidak valid.")
		return false
	}
	return true
}

// write sends JSON that may contain invite tokens or staff emails: never cache it.
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, status, v)
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *domain.ValidationError
	switch {
	case errors.As(err, &ve):
		httpx.ErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed", "Data tidak valid.", ve.Fields)
	case errors.Is(err, domain.ErrLimitReached):
		httpx.Error(w, http.StatusConflict, "facility_limit", "Batas jumlah fasilitas per akun tercapai.")
	case errors.Is(err, domain.ErrLastAdmin):
		httpx.Error(w, http.StatusConflict, "last_admin", "Admin terakhir tidak dapat dihapus. Tambahkan admin lain dulu.")
	case errors.Is(err, domain.ErrAlreadyMember):
		httpx.Error(w, http.StatusConflict, "already_member", "Sudah menjadi anggota fasilitas ini.")
	case errors.Is(err, domain.ErrInvitePending):
		httpx.Error(w, http.StatusConflict, "invite_pending", "Undangan untuk email ini masih aktif.")
	case errors.Is(err, domain.ErrInviteInvalid):
		httpx.Error(w, http.StatusBadRequest, "invite_invalid", "Undangan tidak valid, sudah dipakai, atau kedaluwarsa.")
	case errors.Is(err, domain.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "Data tidak ditemukan.")
	case errors.Is(err, domain.ErrForbidden):
		httpx.Error(w, http.StatusForbidden, "forbidden", "Anda tidak memiliki akses.")
	default:
		slog.Error("facilities request failed", "error", err, "path", r.URL.Path)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan pada server.")
	}
}
