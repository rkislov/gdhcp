// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"reflect"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	"github.com/kislovrs/godhcp/internal/model"
	"github.com/kislovrs/godhcp/internal/storage"
)

// LeaseChange is a compare-and-swap of one IPv4 binding. Before=nil requires
// absence. After=nil removes the binding. Expiry decisions belong to the leader.
type LeaseChange struct {
	IP            string
	Before, After *model.Lease
}
type LeaseCommand struct {
	Version       int
	Changes       []LeaseChange
	IPv6          *storage.IPv6Request
	DUID          []byte
	DNS           *DNSCommand
	Activation    *ActivationCommand
	Configuration *ConfigurationCommand
}

// LeaseState is the deterministic IPv4 portion of the replicated state machine.
// It is not a complete replacement for storage.Store.
type LeaseState struct {
	mu            sync.RWMutex
	index         uint64
	leases        map[string]model.Lease
	v6            map[string]binding6
	duid          []byte
	activation    ActivationState
	configuration ConfigurationImage
	dnsOwner      string
	dns           map[string]storage.DNSRecord
}

func NewLeaseState() *LeaseState {
	return &LeaseState{leases: map[string]model.Lease{}, v6: map[string]binding6{}, dns: map[string]storage.DNSRecord{}}
}

func (s *LeaseState) Apply(log *raft.Log) interface{} {
	var cmd LeaseCommand
	dec := json.NewDecoder(bytes.NewReader(log.Data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cmd); err != nil {
		return err
	}
	if dec.Decode(new(any)) != io.EOF {
		return fmt.Errorf("cluster: trailing command data")
	}
	kinds := 0
	if len(cmd.Changes) > 0 {
		kinds++
	}
	if cmd.IPv6 != nil {
		kinds++
	}
	if cmd.DUID != nil {
		kinds++
	}
	if cmd.DNS != nil {
		kinds++
	}
	if cmd.Activation != nil {
		kinds++
	}
	if cmd.Configuration != nil {
		kinds++
	}
	if cmd.Version != 1 || kinds != 1 || len(cmd.Changes) > 1024 {
		return fmt.Errorf("cluster: invalid lease command")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if log.Index <= s.index {
		return fmt.Errorf("cluster: non-increasing apply index")
	}
	// The applied index advances for rejected commands too. Validation is atomic.
	s.index = log.Index
	if cmd.Configuration != nil {
		return s.applyConfiguration(*cmd.Configuration)
	}
	if cmd.Activation != nil {
		return s.applyActivation(*cmd.Activation)
	}
	if cmd.DNS != nil {
		return s.applyDNS(*cmd.DNS)
	}
	if cmd.IPv6 != nil {
		return s.apply6(*cmd.IPv6)
	}
	if cmd.DUID != nil {
		if len(cmd.DUID) < 3 || len(cmd.DUID) > 128 {
			return fmt.Errorf("cluster: invalid DUID")
		}
		if len(s.duid) == 0 {
			s.duid = append([]byte(nil), cmd.DUID...)
		}
		return append([]byte(nil), s.duid...)
	}
	seen := map[string]bool{}
	for _, c := range cmd.Changes {
		ip, err := netip.ParseAddr(c.IP)
		if err != nil || !ip.Is4() || seen[c.IP] {
			return fmt.Errorf("cluster: invalid or duplicate address")
		}
		seen[c.IP] = true
		old, exists := s.leases[c.IP]
		if (c.Before == nil && exists) || (c.Before != nil && (!exists || !reflect.DeepEqual(old, *c.Before))) {
			// An exact retry after an uncertain commit is safe, but must not overwrite a
			// newer binding. All mutations still require a fresh majority commit.
			if c.After == nil && !exists {
				continue
			}
			if c.After != nil && exists && reflect.DeepEqual(old, *c.After) {
				continue
			}
			return fmt.Errorf("cluster: lease revision conflict for %s", c.IP)
		}
		if c.After != nil {
			if err := validateLease(c.IP, *c.After); err != nil {
				return err
			}
		}
	}
	for _, c := range cmd.Changes {
		if c.After == nil {
			delete(s.leases, c.IP)
		} else {
			s.leases[c.IP] = cloneLease(*c.After)
		}
	}
	return nil
}
func validateLease(ip string, l model.Lease) error {
	if l.IP != ip || l.MAC == "" || l.CreatedAt.IsZero() || l.ExpiresAt.IsZero() {
		return fmt.Errorf("cluster: incomplete lease")
	}
	switch l.State {
	case model.StateOffered, model.StateBound, model.StateReleased, model.StateExpired, model.StateDeclined:
		return nil
	}
	return fmt.Errorf("cluster: invalid lease state")
}
func cloneLease(l model.Lease) model.Lease {
	if l.VLANID != nil {
		v := *l.VLANID
		l.VLANID = &v
	}
	return l
}
func (s *LeaseState) Get(ip string) (model.Lease, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.leases[ip]
	return cloneLease(l), ok
}

type leaseImage struct {
	Version       int
	Index         uint64
	Leases        map[string]model.Lease
	IPv6          map[string]binding6
	DUID          []byte
	Activation    ActivationState
	Configuration ConfigurationImage
	DNSOwner      string
	DNS           map[string]storage.DNSRecord
}

func (s *LeaseState) Snapshot() (raft.FSMSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, e := json.Marshal(leaseImage{Version: 1, Index: s.index, Leases: s.leases, IPv6: s.v6, DUID: s.duid, DNSOwner: s.dnsOwner, DNS: s.dns, Activation: s.activation, Configuration: s.configuration})
	return leaseSnapshot(b), e
}
func (s *LeaseState) Restore(r io.ReadCloser) error {
	defer r.Close()
	var image leaseImage
	dec := json.NewDecoder(io.LimitReader(r, 64*1024*1024+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&image); err != nil {
		return err
	}
	if dec.Decode(new(any)) != io.EOF || image.Version != 1 || image.Leases == nil {
		return fmt.Errorf("cluster: invalid snapshot")
	}
	for ip, l := range image.Leases {
		a, e := netip.ParseAddr(ip)
		if e != nil || !a.Is4() {
			return fmt.Errorf("cluster: invalid snapshot address")
		}
		if e := validateLease(ip, l); e != nil {
			return e
		}
	}
	if len(image.DUID) > 0 && (len(image.DUID) < 3 || len(image.DUID) > 128) {
		return fmt.Errorf("cluster: invalid snapshot DUID")
	}
	if err := validateConfigurationImage(image.Configuration); err != nil {
		return err
	}
	if image.DNSOwner != "" {
		if err := validateDNSOwner(image.DNSOwner); err != nil {
			return err
		}
	}
	for key, r := range image.DNS {
		if key != r.Key() {
			return fmt.Errorf("cluster: invalid DNS snapshot key")
		}
		if err := validateDNSRecord(r); err != nil {
			return err
		}
	}
	owners := map[string]bool{}
	for ip, l := range image.IPv6 {
		a, e := netip.ParseAddr(ip)
		p, pe := netip.ParsePrefix(l.Prefix)
		if e != nil || pe != nil || !a.Is6() || a.Is4In6() || !p.Contains(a) || l.Expires <= 0 {
			return fmt.Errorf("cluster: invalid IPv6 snapshot")
		}
		if l.State != "bound" && l.State != "offered" && l.State != "declined" {
			return fmt.Errorf("cluster: invalid IPv6 state")
		}
		if l.State != "declined" {
			if l.Client == "" {
				return fmt.Errorf("cluster: missing IPv6 owner")
			}
			key := fmt.Sprintf("%s/%s/%d", l.Prefix, l.Client, l.IAID)
			if owners[key] {
				return fmt.Errorf("cluster: duplicate IPv6 owner")
			}
			owners[key] = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leases = image.Leases
	s.v6 = image.IPv6
	if s.v6 == nil {
		s.v6 = map[string]binding6{}
	}
	s.duid = append([]byte(nil), image.DUID...)
	s.activation = image.Activation
	s.configuration = image.Configuration
	s.dnsOwner = image.DNSOwner
	s.dns = image.DNS
	if s.dns == nil {
		s.dns = map[string]storage.DNSRecord{}
	}
	s.index = image.Index
	return nil
}

type leaseSnapshot []byte

func (s leaseSnapshot) Persist(sink raft.SnapshotSink) error {
	if _, err := sink.Write(s); err != nil {
		sink.Cancel()
		return err
	}
	return sink.Close()
}
func (leaseSnapshot) Release() {}

func (n *Node) CommitLeases(changes []LeaseChange, timeout time.Duration) error {
	b, err := json.Marshal(LeaseCommand{Version: 1, Changes: changes})
	if err != nil {
		return err
	}
	return n.Commit(b, timeout)
}
