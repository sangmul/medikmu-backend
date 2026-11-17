package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/sangmul/medikmu-backend/internal/auth/domain"
	"github.com/sangmul/medikmu-backend/internal/platform/authctx"
)

type accessClaims struct {
	FacilityID string `json:"fid,omitempty"`
	Role       string `json:"role,omitempty"`
	jwt.RegisteredClaims
}

// signAccess issues a signed HS256 access token.
func (s *Service) signAccess(userID, facilityID, role string, now time.Time) (string, error) {
	claims := accessClaims{
		FacilityID: facilityID,
		Role:       role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.cfg.Issuer,
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.cfg.AccessTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.cfg.JWTSecret)
}

// VerifyAccessToken validates signature, algorithm, issuer and expiry, and returns the caller.
// Any failure returns domain.ErrInvalidToken.
func (s *Service) VerifyAccessToken(token string) (authctx.Principal, error) {
	var claims accessClaims
	_, err := jwt.ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) {
		return s.cfg.JWTSecret, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(s.cfg.Issuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil || claims.Subject == "" {
		return authctx.Principal{}, domain.ErrInvalidToken
	}
	return authctx.Principal{UserID: claims.Subject, FacilityID: claims.FacilityID, Role: claims.Role}, nil
}

// newRefreshToken returns a random opaque token and its storage hash.
func newRefreshToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
