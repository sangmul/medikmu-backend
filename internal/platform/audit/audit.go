// Package audit records who accessed or changed sensitive data. Every module that touches patient
// data takes a Recorder and calls it before returning data (fail closed: if recording fails, the
// request fails). Never put PII or medical content in an Entry; IDs only.
package audit

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Entry is one audit record.
type Entry struct {
	FacilityID string
	ActorID    string
	Action     string // e.g. "patient.read"
	Resource   string // e.g. "patient"
	ResourceID string // optional
}

// Recorder persists audit entries.
type Recorder interface {
	Record(ctx context.Context, e Entry) error
}

// Postgres writes entries to the audit_logs table (append-only by convention: no update/delete paths exist).
type Postgres struct{ pool *pgxpool.Pool }

// NewPostgres returns a Recorder backed by the given pool.
func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

// Record inserts the entry.
func (p *Postgres) Record(ctx context.Context, e Entry) error {
	var facility, actor, resourceID *string
	if e.FacilityID != "" {
		facility = &e.FacilityID
	}
	if e.ActorID != "" {
		actor = &e.ActorID
	}
	if e.ResourceID != "" {
		resourceID = &e.ResourceID
	}
	_, err := p.pool.Exec(ctx,
		`INSERT INTO audit_logs (facility_id, actor_id, action, resource, resource_id) VALUES ($1, $2, $3, $4, $5)`,
		facility, actor, e.Action, e.Resource, resourceID)
	if err != nil {
		return fmt.Errorf("record audit entry: %w", err)
	}
	return nil
}

// Memory is an in-memory Recorder for tests.
type Memory struct {
	mu      sync.Mutex
	Entries []Entry
	// Err, when set, makes Record fail (to test fail-closed behavior).
	Err error
}

// Record appends the entry unless Err is set.
func (m *Memory) Record(_ context.Context, e Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return m.Err
	}
	m.Entries = append(m.Entries, e)
	return nil
}

// Actions returns the recorded actions in order.
func (m *Memory) Actions() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.Entries))
	for i, e := range m.Entries {
		out[i] = e.Action
	}
	return out
}
