// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package cluster

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// ActivationState retains every possibly active predecessor until fencing has
// been acknowledged by a majority. A crash during takeover cannot erase one.
type ActivationState struct {
	Revision uint64
	Owner    string
	Pending  []string
}
type ActivationCommand struct {
	Expected uint64
	Claim    string
	Fenced   string
}

func (s *LeaseState) applyActivation(c ActivationCommand) interface{} {
	if c.Expected != s.activation.Revision || (c.Claim == "") == (c.Fenced == "") {
		return fmt.Errorf("cluster: activation revision conflict or invalid command")
	}
	a := s.activation
	a.Pending = append([]string(nil), a.Pending...)
	if c.Claim != "" {
		if len(c.Claim) > 128 {
			return fmt.Errorf("cluster: invalid node ID")
		}
		if a.Owner != "" && !slices.Contains(a.Pending, a.Owner) {
			a.Pending = append(a.Pending, a.Owner)
		}
		a.Owner = c.Claim
	} else {
		index := slices.Index(a.Pending, c.Fenced)
		if index < 0 {
			return fmt.Errorf("cluster: unknown fencing target")
		}
		a.Pending = append(a.Pending[:index], a.Pending[index+1:]...)
	}
	a.Revision++
	s.activation = a
	return nil
}
func (s *LeaseState) Activation() ActivationState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a := s.activation
	a.Pending = append([]string(nil), a.Pending...)
	return a
}

// ActivationHooks must stop and join the DHCP/API/DDNS workload independently
// of the Raft process. Fence must confirm isolation of the named workload,
// including existing DNS children and its service IP. Ping failure is not proof.
type ActivationHooks interface {
	Stop(context.Context) error
	Fence(context.Context, string) error
	AcquireIP(context.Context) error
	ReleaseIP(context.Context) error
	Start(context.Context) error
}

// Takeover stops local leftovers, commits an ownership claim, fences every
// uncertain predecessor, and only then acquires the IP and starts the workload.
// The caller must run Maintain after success. Hooks must honor cancellation.
func (n *Node) Takeover(ctx context.Context, id string, state *LeaseState, h ActivationHooks) error {
	if id == "" || id != n.id {
		return fmt.Errorf("cluster: missing identity")
	}
	if e := h.Stop(ctx); e != nil {
		return fmt.Errorf("cluster: local stop: %w", e)
	}
	if e := h.ReleaseIP(ctx); e != nil {
		return fmt.Errorf("cluster: local release: %w", e)
	}
	if e := n.CheckAuthority(ctx); e != nil {
		return e
	}
	before := state.Activation()
	if _, e := n.commitResult(ctx, LeaseCommand{Version: 1, Activation: &ActivationCommand{Expected: before.Revision, Claim: id}}); e != nil {
		return e
	}
	for {
		if e := n.CheckAuthority(ctx); e != nil {
			return e
		}
		a := state.Activation()
		if a.Owner != id {
			return fmt.Errorf("cluster: ownership changed")
		}
		if len(a.Pending) == 0 {
			break
		}
		target := a.Pending[0]
		// Local leftovers were stopped above. Remotely fence other uncertain owners.
		if target != id {
			if e := h.Fence(ctx, target); e != nil {
				return fmt.Errorf("cluster: fencing %s: %w", target, e)
			}
		}
		if _, e := n.commitResult(ctx, LeaseCommand{Version: 1, Activation: &ActivationCommand{Expected: a.Revision, Fenced: target}}); e != nil {
			return e
		}
	}
	if e := n.CheckAuthority(ctx); e != nil {
		return e
	}
	if e := h.AcquireIP(ctx); e != nil {
		return errors.Join(e, cleanupActivation(h))
	}
	if e := n.CheckAuthority(ctx); e != nil {
		return errors.Join(e, cleanupActivation(h))
	}
	if e := h.Start(ctx); e != nil {
		return errors.Join(e, cleanupActivation(h))
	}
	return nil
}

// Maintain fails closed on loss of majority or cancellation. Cleanup failures
// are returned and must terminate the supervisor, never trigger a local retry.
func (n *Node) Maintain(ctx context.Context, h ActivationHooks) error {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return cleanupActivation(h)
		case <-tick.C:
			if e := n.CheckAuthority(ctx); e != nil {
				if stopErr := cleanupActivation(h); stopErr != nil {
					return fmt.Errorf("cluster: authority lost (%v), cleanup failed: %w", e, stopErr)
				}
				return e
			}
		}
	}
}
func cleanupActivation(h ActivationHooks) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// Never release an address while the old workload is still sending replies.
	if e := h.Stop(ctx); e != nil {
		return e
	}
	return h.ReleaseIP(ctx)
}
