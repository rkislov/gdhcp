// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package cluster

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"github.com/kislovrs/godhcp/internal/storage"
	"net/netip"
	"time"
)

type binding6 struct {
	Prefix, Client, State string
	IAID                  uint32
	Expires               int64
}

// apply6 executes only under the state lock, with time supplied in the log.
func (s *LeaseState) apply6(r storage.IPv6Request) interface{} {
	p, e := netip.ParsePrefix(r.Prefix)
	if e != nil || !p.Addr().Is6() || p.Addr().Is4In6() || !r.Start.Is6() || !r.End.Is6() || !p.Contains(r.Start) || !p.Contains(r.End) || r.Start.Compare(r.End) > 0 || r.Client == "" || r.Now.IsZero() || r.Lifetime <= 0 || r.Action < storage.Advertise6 || r.Action > storage.Decline6 {
		return fmt.Errorf("cluster: invalid IPv6 request")
	}
	for ip, l := range s.v6 {
		if l.Expires <= r.Now.Unix() {
			delete(s.v6, ip)
		}
	}
	var ip netip.Addr
	var old binding6
	for address, l := range s.v6 {
		if l.Prefix == r.Prefix && l.Client == r.Client && l.IAID == r.IAID {
			ip = netip.MustParseAddr(address)
			old = l
			break
		}
	}
	expiry := r.Now.Add(r.Lifetime).Unix()
	if r.Action >= storage.Renew6 {
		matches := len(r.Hints) == 0 && r.Action == storage.Renew6
		for _, h := range r.Hints {
			if h == ip {
				matches = true
			}
		}
		if !ip.IsValid() || old.State != "bound" || !matches || (r.Action == storage.Renew6 && (ip.Compare(r.Start) < 0 || ip.Compare(r.End) > 0)) {
			return storage.ErrNoBinding6
		}
		switch r.Action {
		case storage.Release6:
			delete(s.v6, ip.String())
		case storage.Decline6:
			old.State = "declined"
			old.Client = ""
			old.Expires = expiry
			s.v6[ip.String()] = old
		case storage.Renew6:
			old.Expires = expiry
			s.v6[ip.String()] = old
		}
		return ip
	}
	if ip.IsValid() {
		if ip.Compare(r.Start) < 0 || ip.Compare(r.End) > 0 {
			return storage.ErrNoAddress6
		}
		if r.Action == storage.Advertise6 && old.State == "bound" {
			return ip
		}
	} else {
		available := func(a netip.Addr) bool {
			_, used := s.v6[a.String()]
			return a.Is6() && a.Compare(r.Start) >= 0 && a.Compare(r.End) <= 0 && !used
		}
		for _, h := range r.Hints {
			if available(h) {
				ip = h
				break
			}
		}
		if !ip.IsValid() {
			for a := r.Start; a.IsValid() && a.Compare(r.End) <= 0; a = a.Next() {
				if available(a) {
					ip = a
					break
				}
			}
		}
		if !ip.IsValid() {
			return storage.ErrNoAddress6
		}
	}
	state := "offered"
	if r.Action == storage.Bind6 {
		state = "bound"
	}
	s.v6[ip.String()] = binding6{r.Prefix, r.Client, state, r.IAID, expiry}
	return ip
}

func (n *Node) commitResult(ctx context.Context, command LeaseCommand) (interface{}, error) {
	b, e := json.Marshal(command)
	if e != nil {
		return nil, e
	}
	if len(b) > 1024*1024 {
		return nil, fmt.Errorf("cluster: command too large")
	}
	if n.pair != nil {
		return n.pair.commit(ctx, b)
	}
	f := n.Raft.Apply(b, 3*time.Second)
	done := make(chan struct{})
	go func() { f.Error(); close(done) }()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("cluster: commit outcome unknown")
	case <-done:
	}
	if e := f.Error(); e != nil {
		return nil, e
	}
	if e, ok := f.Response().(error); ok {
		return nil, e
	}
	return f.Response(), nil
}
func (s *LeaseStore) InitIPv6(ctx context.Context, configured []byte) ([]byte, error) {
	if e := s.CheckAuthority(ctx); e != nil {
		return nil, e
	}
	s.State.mu.RLock()
	saved := append([]byte(nil), s.State.duid...)
	s.State.mu.RUnlock()
	if len(saved) > 0 {
		if len(configured) > 0 && !bytes.Equal(saved, configured) {
			return nil, fmt.Errorf("cluster: configured DUID differs from cluster")
		}
		return saved, nil
	}
	id := append([]byte(nil), configured...)
	if len(id) == 0 {
		id = make([]byte, 18)
		id[1] = 4
		if _, e := rand.Read(id[2:]); e != nil {
			return nil, e
		}
		id[8] = id[8]&15 | 64
		id[10] = id[10]&63 | 128
	}
	result, e := s.Node.commitResult(ctx, LeaseCommand{Version: 1, DUID: id})
	if e != nil {
		return nil, e
	}
	saved = result.([]byte)
	if len(configured) > 0 && !bytes.Equal(saved, configured) {
		return nil, fmt.Errorf("cluster: DUID initialization conflict")
	}
	return saved, nil
}
func (s *LeaseStore) ApplyIPv6(ctx context.Context, r storage.IPv6Request) (netip.Addr, error) {
	if e := s.CheckAuthority(ctx); e != nil {
		return netip.Addr{}, e
	}
	result, e := s.Node.commitResult(ctx, LeaseCommand{Version: 1, IPv6: &r})
	if e != nil {
		return netip.Addr{}, e
	}
	return result.(netip.Addr), nil
}

var _ storage.IPv6Store = (*LeaseStore)(nil)
