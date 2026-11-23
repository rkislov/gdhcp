// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kislovrs/godhcp/internal/model"
	"github.com/kislovrs/godhcp/internal/storage"
)

// LeaseStore adapts replicated IPv4 bindings to the DHCP core. Catalog remains
// supplied by the caller and must be initialized from the same configuration on
// every node. This adapter alone does not provide configuration replication.
type LeaseStore struct {
	storage.Store
	Node  *Node
	State *LeaseState
}

func (s *LeaseStore) CheckAuthority(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.Node.CheckAuthority(ctx)
}
func (n *Node) CheckAuthority(ctx context.Context) error {
	if n.pair != nil {
		return n.pair.authority(ctx)
	}
	done := make(chan error, 1)
	go func() {
		if e := n.Raft.VerifyLeader().Error(); e != nil {
			done <- e
			return
		}
		done <- n.Raft.Barrier(2 * time.Second).Error()
	}()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case e := <-done:
		return e
	case <-timer.C:
		return fmt.Errorf("cluster: authority check timeout")
	}
}
func (s *LeaseStore) Ping(ctx context.Context) error { return s.CheckAuthority(ctx) }
func (s *LeaseStore) UpsertLease(ctx context.Context, l model.Lease) error {
	if err := s.CheckAuthority(ctx); err != nil {
		return err
	}
	if l.CreatedAt.IsZero() {
		l.CreatedAt = time.Now().UTC()
	}
	old, ok := s.State.Get(l.IP)
	var before *model.Lease
	if ok {
		before = &old
	}
	// The core serializes selection and mutation. Refuse changing the owner of
	// a live binding even if a caller's in-memory pool has become stale.
	if ok && old.ActiveAt(time.Now()) && (old.MAC != l.MAC || old.ClientID != l.ClientID) {
		return fmt.Errorf("cluster: address already owned")
	}
	return s.Node.CommitLeases([]LeaseChange{{IP: l.IP, Before: before, After: &l}}, 3*time.Second)
}
func (s *LeaseStore) GetLease(ctx context.Context, ip string) (model.Lease, error) {
	if err := s.CheckAuthority(ctx); err != nil {
		return model.Lease{}, err
	}
	l, ok := s.State.Get(ip)
	if !ok {
		return l, storage.ErrNotFound
	}
	return l, nil
}
func (s *LeaseStore) DeleteLease(ctx context.Context, ip string) error {
	l, e := s.GetLease(ctx, ip)
	if e != nil {
		return e
	}
	return s.Node.CommitLeases([]LeaseChange{{IP: ip, Before: &l}}, 3*time.Second)
}
func (s *LeaseStore) all(ctx context.Context) ([]model.Lease, error) {
	if err := s.CheckAuthority(ctx); err != nil {
		return nil, err
	}
	s.State.mu.RLock()
	defer s.State.mu.RUnlock()
	out := make([]model.Lease, 0, len(s.State.leases))
	for _, l := range s.State.leases {
		out = append(out, cloneLease(l))
	}
	return out, nil
}
func (s *LeaseStore) ActiveLeases(ctx context.Context, now time.Time) ([]model.Lease, error) {
	all, e := s.all(ctx)
	if e != nil {
		return nil, e
	}
	out := []model.Lease{}
	for _, l := range all {
		if l.ActiveAt(now) {
			out = append(out, l)
		}
	}
	return out, nil
}
func (s *LeaseStore) ExpireLeases(ctx context.Context, now time.Time) (int, error) {
	all, e := s.all(ctx)
	if e != nil {
		return 0, e
	}
	changes := []LeaseChange{}
	for _, l := range all {
		if (l.State == model.StateBound || l.State == model.StateOffered || l.State == model.StateDeclined) && !l.ExpiresAt.After(now) {
			old := l
			l.State = model.StateExpired
			changes = append(changes, LeaseChange{IP: l.IP, Before: &old, After: &l})
			if len(changes) == 1024 {
				break
			}
		}
	}
	if len(changes) == 0 {
		return 0, nil
	}
	if e := s.Node.CommitLeases(changes, 3*time.Second); e != nil {
		return 0, e
	}
	return len(changes), nil
}
func (s *LeaseStore) ListLeases(ctx context.Context, f model.LeaseFilter) ([]model.Lease, int, error) {
	all, e := s.all(ctx)
	if e != nil {
		return nil, 0, e
	}
	out := []model.Lease{}
	contains := func(items []string, x string) bool {
		for _, v := range items {
			if v == x {
				return true
			}
		}
		return false
	}
	for _, l := range all {
		if f.AllowedSubnets != nil && !contains(f.AllowedSubnets, l.SubnetID) {
			continue
		}
		if f.AllowedVLANs != nil && l.VLANID != nil {
			allowed := false
			for _, v := range f.AllowedVLANs {
				if v == *l.VLANID {
					allowed = true
				}
			}
			if !allowed {
				continue
			}
		}
		if f.VLAN != nil && (l.VLANID == nil || *l.VLANID != *f.VLAN) {
			continue
		}
		if f.SubnetID != "" && f.SubnetID != l.SubnetID || f.GIAddr != "" && f.GIAddr != l.GIAddr || f.State != "" && f.State != l.State {
			continue
		}
		if f.Query != "" {
			q := strings.ToLower(f.Query)
			if !strings.Contains(strings.ToLower(l.IP+" "+l.MAC+" "+l.Hostname+" "+l.CircuitID), q) {
				continue
			}
		}
		out = append(out, l)
	}
	page, total := model.LeasePage(out, f)
	return page, total, nil
}

var _ storage.Store = (*LeaseStore)(nil)
