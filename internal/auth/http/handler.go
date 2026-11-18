// Package authhttp exposes the auth module over HTTP and provides the authentication middleware.
package authhttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/sangmul/medikmu-backend/internal/auth/domain"
	"github.com/sangmul/medikmu-backend/internal/auth/service"
	"github.com/sangmul/medikmu-backend/internal/platform/authctx"
	"github.com/sangmul/medikmu-backend/internal/platform/httpx"
)

// Auth is the subset of the auth service the HTTP layer needs.
type Auth interface {
	Register(ctx context.Context, in service.RegisterInput) (domain.TokenPair, error)
	Login(ctx context.Context, email, password string) (domain.TokenPair, error)
	Refresh(ctx context.Context, refreshToken string) (domain.TokenPair, error)
	Logout(ctx context.Context, refreshToken string) error
	SwitchFacility(ctx context.Context, userID, refreshToken, facilityID string) (domain.TokenPair, error)
	Me(ctx context.Context, userID string) (domain.Me, error)
	VerifyAccessToken(token string) (authctx.Principal, error)
}

// Handler serves the auth endpoints.
type Handler struct {
	auth  Auth
	limit func(http.Handler) http.Handler // applied to credential-guessing endpoints; nil = none
}

// Option customizes a Handler.
type Option func(*Handler)

// WithRateLimit wraps login, register and refresh (the endpoints attackers hammer) in mw.
func WithRateLimit(mw func(http.Handler) http.Handler) Option {
	return func(h *Handler) { h.limit = mw }
}

// New returns a Handler.
func New(a Auth, opts ...Option) *Handler {
	h := &Handler{auth: a}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Mount registers the auth routes.
func (h *Handler) Mount(mux *http.ServeMux) {
	limited := func(f http.HandlerFunc) http.Handler {
		if h.limit == nil {
			return f
		}
		return h.limit(f)
	}
	mux.Handle("POST /api/v1/auth/register", limited(h.register))
	mux.Handle("POST /api/v1/auth/login", limited(h.login))
	mux.Handle("POST /api/v1/auth/refresh", limited(h.refresh))
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)

	protected := httpx.RequireAuth(h.auth.VerifyAccessToken)
	mux.Handle("POST /api/v1/auth/switch-facility", protected(http.HandlerFunc(h.switchFacility)))
	mux.Handle("GET /api/v1/me", protected(http.HandlerFunc(h.me)))
}

// --- request / response bodies (mirror api/openapi.yaml) ---

type registerRequest struct {
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Password string `json:"password"`
	FullName string `json:"full_name"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type switchFacilityRequest struct {
	RefreshToken string `json:"refresh_token"`
	FacilityID   string `json:"facility_id"` // empty = personal context
}

type tokenPairResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type membershipResponse struct {
	FacilityID string `json:"facility_id"`
	Role       string `json:"role"`
}

type meResponse struct {
	ID          string               `json:"id"`
	Email       string               `json:"email"`
	Phone       string               `json:"phone,omitempty"`
	FullName    string               `json:"full_name"`
	Memberships []membershipResponse `json:"memberships"`
}

// --- handlers ---

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decode(w, r, &req) {
		return
	}
	pair, err := h.auth.Register(r.Context(), service.RegisterInput{
		Email: req.Email, Phone: req.Phone, Password: req.Password, FullName: req.FullName,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writePair(w, http.StatusCreated, pair)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decode(w, r, &req) {
		return
	}
	pair, err := h.auth.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writePair(w, http.StatusOK, pair)
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !decode(w, r, &req) {
		return
	}
	pair, err := h.auth.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writePair(w, http.StatusOK, pair)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !decode(w, r, &req) {
		return
	}
	if err := h.auth.Logout(r.Context(), req.RefreshToken); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) switchFacility(w http.ResponseWriter, r *http.Request) {
	var req switchFacilityRequest
	if !decode(w, r, &req) {
		return
	}
	p, _ := authctx.From(r.Context())
	pair, err := h.auth.SwitchFacility(r.Context(), p.UserID, req.RefreshToken, req.FacilityID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writePair(w, http.StatusOK, pair)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	p, _ := authctx.From(r.Context())
	me, err := h.auth.Me(r.Context(), p.UserID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp := meResponse{
		ID: me.User.ID, Email: me.User.Email, Phone: me.User.Phone, FullName: me.User.FullName,
		Memberships: make([]membershipResponse, 0, len(me.Memberships)),
	}
	for _, m := range me.Memberships {
		resp.Memberships = append(resp.Memberships, membershipResponse{FacilityID: m.FacilityID, Role: m.Role})
	}
	noStore(w)
	httpx.JSON(w, http.StatusOK, resp)
}

// --- helpers ---

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := httpx.DecodeJSON(w, r, dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Permintaan tidak valid.")
		return false
	}
	return true
}

func writePair(w http.ResponseWriter, status int, p domain.TokenPair) {
	noStore(w)
	httpx.JSON(w, status, tokenPairResponse{AccessToken: p.AccessToken, RefreshToken: p.RefreshToken, ExpiresIn: p.ExpiresIn})
}

// noStore keeps tokens and account data out of shared caches.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

// writeError maps domain errors to the standard error envelope. Unknown errors are logged and returned as 500
// without detail.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *domain.ValidationError
	switch {
	case errors.As(err, &ve):
		httpx.ErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed", "Data tidak valid.", ve.Fields)
	case errors.Is(err, domain.ErrEmailTaken):
		httpx.Error(w, http.StatusConflict, "email_taken", "Email sudah terdaftar.")
	case errors.Is(err, domain.ErrPhoneTaken):
		httpx.Error(w, http.StatusConflict, "phone_taken", "Nomor telepon sudah terdaftar.")
	case errors.Is(err, domain.ErrInvalidCredentials):
		httpx.Error(w, http.StatusUnauthorized, "invalid_credentials", "Email atau kata sandi salah.")
	case errors.Is(err, domain.ErrInvalidToken), errors.Is(err, domain.ErrNotFound):
		httpx.Error(w, http.StatusUnauthorized, "invalid_token", "Sesi tidak valid atau sudah berakhir. Silakan masuk lagi.")
	case errors.Is(err, domain.ErrForbidden):
		httpx.Error(w, http.StatusForbidden, "forbidden", "Anda tidak memiliki akses.")
	default:
		slog.Error("auth request failed", "error", err, "path", r.URL.Path)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan pada server.")
	}
}
