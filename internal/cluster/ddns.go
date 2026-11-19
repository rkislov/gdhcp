// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package cluster

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/kislovrs/godhcp/internal/storage"
	"reflect"
	"sort"
	"strings"
)

type DNSCommand struct {
	Owner         string
	Key           string
	Before, After *storage.DNSRecord
}

func validateDNSOwner(owner string) error {
	b, e := hex.DecodeString(owner)
	if e != nil || len(b) != 32 {
		return fmt.Errorf("cluster: invalid DNS owner")
	}
	return nil
}
func validateDNSRecord(r storage.DNSRecord) error {
	if r.Server == "" || r.Zone == "" || r.Name == "" || r.Value == "" || r.Token == "" || r.TTL == 0 || r.LeaseExpires.IsZero() {
		return fmt.Errorf("cluster: incomplete DNS intent")
	}
	if r.Type != "A" && r.Type != "PTR" {
		return fmt.Errorf("cluster: unsupported DNS record")
	}
	for _, part := range []string{r.Server, r.Zone, r.Name, r.Type} {
		if strings.ContainsAny(part, "|\x00\r\n") {
			return fmt.Errorf("cluster: invalid DNS key component")
		}
	}
	return nil
}
func (s *LeaseState) applyDNS(c DNSCommand) interface{} {
	if c.Owner != "" {
		if c.Key != "" || c.Before != nil || c.After != nil {
			return fmt.Errorf("cluster: mixed DNS command")
		}
		if e := validateDNSOwner(c.Owner); e != nil {
			return e
		}
		if s.dnsOwner == "" {
			s.dnsOwner = c.Owner
		}
		return s.dnsOwner
	}
	if c.Key == "" || s.dnsOwner == "" {
		return fmt.Errorf("cluster: uninitialized DNS state")
	}
	if c.After != nil {
		if e := validateDNSRecord(*c.After); e != nil {
			return e
		}
		if c.After.Key() != c.Key {
			return fmt.Errorf("cluster: DNS key mismatch")
		}
	}
	old, exists := s.dns[c.Key]
	if (c.Before == nil && exists) || (c.Before != nil && (!exists || !reflect.DeepEqual(old, *c.Before))) {
		if c.After == nil && !exists {
			return nil
		}
		if c.After != nil && exists && reflect.DeepEqual(old, *c.After) {
			return nil
		}
		return fmt.Errorf("cluster: DNS revision conflict")
	}
	if c.After == nil {
		delete(s.dns, c.Key)
	} else {
		s.dns[c.Key] = *c.After
	}
	return nil
}
func (s *LeaseStore) InitDNS(ctx context.Context) (string, error) {
	if e := s.CheckAuthority(ctx); e != nil {
		return "", e
	}
	s.State.mu.RLock()
	owner := s.State.dnsOwner
	s.State.mu.RUnlock()
	if owner != "" {
		return owner, nil
	}
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	result, e := s.Node.commitResult(ctx, LeaseCommand{Version: 1, DNS: &DNSCommand{Owner: hex.EncodeToString(b)}})
	if e != nil {
		return "", e
	}
	return result.(string), nil
}
func (s *LeaseStore) DNSRecords(ctx context.Context) ([]storage.DNSRecord, error) {
	if e := s.CheckAuthority(ctx); e != nil {
		return nil, e
	}
	s.State.mu.RLock()
	defer s.State.mu.RUnlock()
	out := make([]storage.DNSRecord, 0, len(s.State.dns))
	for _, r := range s.State.dns {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out, nil
}
func (s *LeaseStore) changeDNS(ctx context.Context, key string, next *storage.DNSRecord) error {
	if e := s.CheckAuthority(ctx); e != nil {
		return e
	}
	s.State.mu.RLock()
	old, ok := s.State.dns[key]
	s.State.mu.RUnlock()
	var before *storage.DNSRecord
	if ok {
		before = &old
	}
	_, e := s.Node.commitResult(ctx, LeaseCommand{Version: 1, DNS: &DNSCommand{Key: key, Before: before, After: next}})
	return e
}
func (s *LeaseStore) PutDNSRecord(ctx context.Context, r storage.DNSRecord) error {
	return s.changeDNS(ctx, r.Key(), &r)
}
func (s *LeaseStore) DeleteDNSRecord(ctx context.Context, key string) error {
	return s.changeDNS(ctx, key, nil)
}

var _ storage.DNSStore = (*LeaseStore)(nil)
