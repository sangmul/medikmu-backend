// Package authtest provides an in-memory domain.Store for tests of the auth module.
package authtest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sangmul/medikmu-backend/internal/auth/domain"
)

// MemStore is a goroutine-safe in-memory implementation of domain.Store.
type MemStore struct {
	mu          sync.Mutex
	seq         int
	users       map[string]domain.User // by id
	memberships map[string][]domain.Membership
	tokens      map[string]domain.RefreshToken // by id
}

// NewMemStore returns an empty store.
func NewMemStore() *MemStore {
	return &MemStore{
		users:       map[string]domain.User{},
		memberships: map[string][]domain.Membership{},
		tokens:      map[string]domain.RefreshToken{},
	}
}

func (m *MemStore) nextID() string {
	m.seq++
	return fmt.Sprintf("id-%d", m.seq)
}

// AddMembership grants a user a role in a facility (test setup helper).
func (m *MemStore) AddMembership(userID, facilityID, role string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.memberships[userID] = append(m.memberships[userID], domain.Membership{FacilityID: facilityID, Role: role})
}

// RemoveMembership revokes a user's role in a facility (test setup helper).
func (m *MemStore) RemoveMembership(userID, facilityID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.memberships[userID][:0:0]
	for _, ms := range m.memberships[userID] {
		if ms.FacilityID != facilityID {
			kept = append(kept, ms)
		}
	}
	m.memberships[userID] = kept
}

// TokenCount returns the number of stored refresh tokens (test helper).
func (m *MemStore) TokenCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.tokens)
}

// RevokedCount returns how many stored refresh tokens are revoked (test helper).
func (m *MemStore) RevokedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, t := range m.tokens {
		if t.RevokedAt != nil {
			n++
		}
	}
	return n
}

func (m *MemStore) CreateUser(_ context.Context, u domain.User) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.users {
		if e.Email == u.Email {
			return domain.User{}, domain.ErrEmailTaken
		}
		if u.Phone != "" && e.Phone == u.Phone {
			return domain.User{}, domain.ErrPhoneTaken
		}
	}
	u.ID = m.nextID()
	u.CreatedAt = time.Now()
	m.users[u.ID] = u
	return u, nil
}

func (m *MemStore) UserByEmail(_ context.Context, email string) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.Email == email {
			return u, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}

func (m *MemStore) UserByID(_ context.Context, id string) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.users[id]; ok {
		return u, nil
	}
	return domain.User{}, domain.ErrNotFound
}

func (m *MemStore) UpdateProfile(_ context.Context, userID, fullName, phone string) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	if phone != "" {
		for id, e := range m.users {
			if id != userID && e.Phone == phone {
				return domain.User{}, domain.ErrPhoneTaken
			}
		}
	}
	if u.Phone != phone {
		u.PhoneVerified = false
	}
	u.FullName, u.Phone = fullName, phone
	m.users[userID] = u
	return u, nil
}

func (m *MemStore) Memberships(_ context.Context, userID string) ([]domain.Membership, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]domain.Membership(nil), m.memberships[userID]...), nil
}

func (m *MemStore) InsertRefreshToken(_ context.Context, t domain.RefreshToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t.ID = m.nextID()
	m.tokens[t.ID] = t
	return nil
}

func (m *MemStore) RefreshTokenByHash(_ context.Context, hash string) (domain.RefreshToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tokens {
		if t.TokenHash == hash {
			return t, nil
		}
	}
	return domain.RefreshToken{}, domain.ErrNotFound
}

func (m *MemStore) MarkRefreshTokenUsed(_ context.Context, id string, at time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[id]
	if !ok || t.UsedAt != nil {
		return false, nil
	}
	t.UsedAt = &at
	m.tokens[id] = t
	return true, nil
}

func (m *MemStore) RevokeFamily(_ context.Context, familyID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, t := range m.tokens {
		if t.FamilyID == familyID && t.RevokedAt == nil {
			t.RevokedAt = &at
			m.tokens[id] = t
		}
	}
	return nil
}
