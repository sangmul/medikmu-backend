// Package facilitiestest provides an in-memory domain.Store for tests of the facilities module.
package facilitiestest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sangmul/medikmu-backend/internal/facilities/domain"
)

type user struct{ name, email string }

type invite struct {
	domain.Invite
	acceptedAt, revokedAt *time.Time
}

// MemStore is a goroutine-safe in-memory domain.Store with the same rules as Postgres.
type MemStore struct {
	mu         sync.Mutex
	seq        int
	users      map[string]user
	facilities map[string]domain.Facility
	members    map[string]map[string]domain.Member // facility -> user -> member
	invites    map[string]*invite
}

// NewMemStore returns an empty store.
func NewMemStore() *MemStore {
	return &MemStore{
		users: map[string]user{}, facilities: map[string]domain.Facility{},
		members: map[string]map[string]domain.Member{}, invites: map[string]*invite{},
	}
}

func (m *MemStore) id(prefix string) string {
	m.seq++
	return fmt.Sprintf("%s0000000-0000-4000-8000-%012d", prefix, m.seq)
}

// AddUser registers an account (test setup) and returns its ID.
func (m *MemStore) AddUser(name, email string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.id("a")
	m.users[id] = user{name, strings.ToLower(email)}
	return id
}

// Role returns the user's role in a facility ("" if not a member). Test helper.
func (m *MemStore) Role(facilityID, userID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.members[facilityID][userID].Role
}

func (m *MemStore) CreateFacility(_ context.Context, f domain.Facility) (domain.Facility, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := 0
	for _, e := range m.facilities {
		if e.CreatedBy == f.CreatedBy {
			owned++
		}
	}
	if owned >= domain.MaxFacilitiesPerUser {
		return domain.Facility{}, domain.ErrLimitReached
	}
	f.ID = m.id("b")
	f.CreatedAt = time.Unix(1_700_000_000, 0).UTC()
	f.UpdatedAt = f.CreatedAt
	m.facilities[f.ID] = f
	u := m.users[f.CreatedBy]
	m.members[f.ID] = map[string]domain.Member{
		f.CreatedBy: {UserID: f.CreatedBy, FullName: u.name, Email: u.email, Role: domain.RoleAdmin, JoinedAt: f.CreatedAt},
	}
	return f, nil
}

func (m *MemStore) FacilityByID(_ context.Context, id string) (domain.Facility, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if f, ok := m.facilities[id]; ok {
		return f, nil
	}
	return domain.Facility{}, domain.ErrNotFound
}

func (m *MemStore) UpdateFacility(_ context.Context, f domain.Facility) (domain.Facility, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.facilities[f.ID]
	if !ok {
		return domain.Facility{}, domain.ErrNotFound
	}
	cur.Name, cur.Phone, cur.Address = f.Name, f.Phone, f.Address // kind and creator are immutable
	cur.UpdatedAt = cur.UpdatedAt.Add(time.Second)
	m.facilities[f.ID] = cur
	return cur, nil
}

func (m *MemStore) Members(_ context.Context, facilityID string) ([]domain.Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Member
	for _, mem := range m.members[facilityID] {
		out = append(out, mem)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out, nil
}

func (m *MemStore) RemoveMember(_ context.Context, facilityID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mem, ok := m.members[facilityID][userID]
	if !ok {
		return domain.ErrNotFound
	}
	if mem.Role == domain.RoleAdmin {
		admins := 0
		for _, o := range m.members[facilityID] {
			if o.Role == domain.RoleAdmin {
				admins++
			}
		}
		if admins == 1 {
			return domain.ErrLastAdmin
		}
	}
	delete(m.members[facilityID], userID)
	return nil
}

func (m *MemStore) CreateInvite(_ context.Context, inv domain.Invite) (domain.Invite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mem := range m.members[inv.FacilityID] {
		if mem.Email == inv.Email {
			return domain.Invite{}, domain.ErrAlreadyMember
		}
	}
	for _, e := range m.invites {
		if e.FacilityID == inv.FacilityID && e.Email == inv.Email && e.acceptedAt == nil && e.revokedAt == nil {
			return domain.Invite{}, domain.ErrInvitePending
		}
	}
	inv.ID = m.id("c")
	inv.CreatedAt = time.Unix(1_700_000_000, 0).UTC()
	m.invites[inv.ID] = &invite{Invite: inv}
	return inv, nil
}

func (m *MemStore) PendingInvites(_ context.Context, facilityID string, now time.Time) ([]domain.Invite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Invite
	for _, e := range m.invites {
		if e.FacilityID == facilityID && e.acceptedAt == nil && e.revokedAt == nil && e.ExpiresAt.After(now) {
			out = append(out, e.Invite)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *MemStore) RevokeInvite(_ context.Context, facilityID, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.invites[id]
	if !ok || e.FacilityID != facilityID || e.acceptedAt != nil || e.revokedAt != nil {
		return domain.ErrNotFound
	}
	e.revokedAt = &at
	return nil
}

func (m *MemStore) InviteByHash(_ context.Context, hash string) (domain.Invite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.invites {
		if e.TokenHash == hash {
			return e.Invite, nil
		}
	}
	return domain.Invite{}, domain.ErrNotFound
}

func (m *MemStore) AcceptInvite(_ context.Context, inviteID, userID string, at time.Time) (domain.Invite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.invites[inviteID]
	if !ok || e.acceptedAt != nil || e.revokedAt != nil || !e.ExpiresAt.After(at) {
		return domain.Invite{}, domain.ErrInviteInvalid
	}
	if _, dup := m.members[e.FacilityID][userID]; dup {
		return domain.Invite{}, domain.ErrAlreadyMember
	}
	u := m.users[userID]
	m.members[e.FacilityID][userID] = domain.Member{UserID: userID, FullName: u.name, Email: u.email, Role: e.Role, JoinedAt: at}
	e.acceptedAt = &at
	return e.Invite, nil
}

func (m *MemStore) UserEmail(_ context.Context, userID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.users[userID]; ok {
		return u.email, nil
	}
	return "", domain.ErrNotFound
}
