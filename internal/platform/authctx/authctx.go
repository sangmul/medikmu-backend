// Package authctx carries the authenticated caller through a request context.
// It is deliberately tiny and domain-free so every module can depend on it.
package authctx

import "context"

// Principal is the authenticated caller, derived from a verified access token.
type Principal struct {
	UserID     string
	FacilityID string // empty when no facility is active (e.g. a patient on mobile)
	Role       string // role within FacilityID; empty when FacilityID is empty
}

// HasFacility reports whether a facility context is active.
func (p Principal) HasFacility() bool { return p.FacilityID != "" }

type ctxKey struct{}

// With returns a context carrying p.
func With(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// From returns the principal stored in ctx.
func From(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}
