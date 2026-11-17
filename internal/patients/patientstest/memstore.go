// Package patientstest provides an in-memory domain.Store for tests of the patients module.
package patientstest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sangmul/medikmu-backend/internal/patients/domain"
)

type row struct {
	p        domain.Patient
	archived bool
}

// MemStore is a goroutine-safe in-memory domain.Store with the same facility scoping as Postgres.
type MemStore struct {
	mu       sync.Mutex
	seq      int
	counters map[string]int
	rows     map[string]*row // by id
}

// NewMemStore returns an empty store.
func NewMemStore() *MemStore {
	return &MemStore{counters: map[string]int{}, rows: map[string]*row{}}
}

func (m *MemStore) nikTaken(facilityID, nik, exceptID string) bool {
	if nik == "" {
		return false
	}
	for id, r := range m.rows {
		if id != exceptID && !r.archived && r.p.FacilityID == facilityID && r.p.NIK == nik {
			return true
		}
	}
	return false
}

func (m *MemStore) Create(_ context.Context, facilityID string, p domain.Patient) (domain.Patient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.nikTaken(facilityID, p.NIK, "") {
		return domain.Patient{}, domain.ErrNIKTaken
	}
	m.seq++
	m.counters[facilityID]++
	p.ID = fmt.Sprintf("00000000-0000-4000-8000-%012d", m.seq)
	p.FacilityID = facilityID
	p.MRN = fmt.Sprintf("RM-%06d", m.counters[facilityID])
	p.CreatedAt = time.Unix(1_700_000_000+int64(m.seq), 0).UTC() // strictly increasing
	p.UpdatedAt = p.CreatedAt
	m.rows[p.ID] = &row{p: p}
	return p, nil
}

func (m *MemStore) find(facilityID, id string) (*row, bool) {
	r, ok := m.rows[id]
	if !ok || r.archived || r.p.FacilityID != facilityID {
		return nil, false
	}
	return r, true
}

func (m *MemStore) Get(_ context.Context, facilityID, id string) (domain.Patient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.find(facilityID, id); ok {
		return r.p, nil
	}
	return domain.Patient{}, domain.ErrNotFound
}

func (m *MemStore) Update(_ context.Context, facilityID string, p domain.Patient) (domain.Patient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.find(facilityID, p.ID)
	if !ok {
		return domain.Patient{}, domain.ErrNotFound
	}
	if m.nikTaken(facilityID, p.NIK, p.ID) {
		return domain.Patient{}, domain.ErrNIKTaken
	}
	// Immutable fields are kept.
	p.FacilityID, p.MRN, p.UserID = r.p.FacilityID, r.p.MRN, r.p.UserID
	p.ConsentAt, p.CreatedBy, p.CreatedAt = r.p.ConsentAt, r.p.CreatedBy, r.p.CreatedAt
	p.UpdatedAt = r.p.UpdatedAt.Add(time.Second)
	r.p = p
	return p, nil
}

func (m *MemStore) Archive(_ context.Context, facilityID, id string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.find(facilityID, id)
	if !ok {
		return domain.ErrNotFound
	}
	r.archived = true
	return nil
}

func (m *MemStore) List(_ context.Context, facilityID string, f domain.ListFilter) ([]domain.Patient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := strings.ToLower(f.Query)
	var out []domain.Patient
	for _, r := range m.rows {
		p := r.p
		if r.archived || p.FacilityID != facilityID {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(p.FullName), q) &&
			!strings.HasPrefix(strings.ToLower(p.MRN), q) && !strings.Contains(p.Phone, q) {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { // newest first, id as tiebreaker
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if f.Cursor != nil {
		i := sort.Search(len(out), func(i int) bool {
			if !out[i].CreatedAt.Equal(f.Cursor.CreatedAt) {
				return out[i].CreatedAt.Before(f.Cursor.CreatedAt)
			}
			return out[i].ID < f.Cursor.ID
		})
		out = out[i:]
	}
	if len(out) > f.Limit+1 {
		out = out[:f.Limit+1]
	}
	return out, nil
}
