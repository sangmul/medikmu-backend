// Package familytest provides an in-memory domain.Store for tests of the family module.
package familytest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/sangmul/medikmu-backend/internal/family/domain"
)

// MemStore is a goroutine-safe in-memory domain.Store with the same scoping and rules as Postgres.
type MemStore struct {
	mu   sync.Mutex
	seq  int
	rows map[string]domain.Member
}

// NewMemStore returns an empty store.
func NewMemStore() *MemStore { return &MemStore{rows: map[string]domain.Member{}} }

func (m *MemStore) nikTaken(userID, nik, exceptID string) bool {
	if nik == "" {
		return false
	}
	for id, r := range m.rows {
		if id != exceptID && r.UserID == userID && r.NIK == nik {
			return true
		}
	}
	return false
}

func (m *MemStore) List(_ context.Context, userID string) ([]domain.Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []domain.Member{}
	for _, r := range m.rows {
		if r.UserID == userID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) Get(_ context.Context, userID, id string) (domain.Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rows[id]; ok && r.UserID == userID {
		return r, nil
	}
	return domain.Member{}, domain.ErrNotFound
}

func (m *MemStore) Create(_ context.Context, userID string, mem domain.Member) (domain.Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, r := range m.rows {
		if r.UserID == userID {
			count++
		}
	}
	if count >= domain.MaxMembersPerUser {
		return domain.Member{}, domain.ErrLimitReached
	}
	if m.nikTaken(userID, mem.NIK, "") {
		return domain.Member{}, domain.ErrNIKTaken
	}
	m.seq++
	mem.ID = fmt.Sprintf("00000000-0000-4000-8000-%012d", m.seq)
	mem.UserID = userID
	mem.CreatedAt = time.Unix(1_700_000_000+int64(m.seq), 0).UTC()
	mem.UpdatedAt = mem.CreatedAt
	m.rows[mem.ID] = mem
	return mem, nil
}

func (m *MemStore) Update(_ context.Context, userID string, mem domain.Member) (domain.Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.rows[mem.ID]
	if !ok || cur.UserID != userID {
		return domain.Member{}, domain.ErrNotFound
	}
	if m.nikTaken(userID, mem.NIK, mem.ID) {
		return domain.Member{}, domain.ErrNIKTaken
	}
	mem.UserID, mem.CreatedAt = cur.UserID, cur.CreatedAt
	mem.UpdatedAt = cur.UpdatedAt.Add(time.Second)
	m.rows[mem.ID] = mem
	return mem, nil
}

func (m *MemStore) SetNIK(_ context.Context, userID, id, nik string) (domain.Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.rows[id]
	if !ok || cur.UserID != userID {
		return domain.Member{}, domain.ErrNotFound
	}
	if m.nikTaken(userID, nik, id) {
		return domain.Member{}, domain.ErrNIKTaken
	}
	cur.NIK = nik
	cur.UpdatedAt = cur.UpdatedAt.Add(time.Second)
	m.rows[id] = cur
	return cur, nil
}

func (m *MemStore) Delete(_ context.Context, userID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rows[id]; ok && r.UserID == userID {
		delete(m.rows, id)
		return nil
	}
	return domain.ErrNotFound
}
