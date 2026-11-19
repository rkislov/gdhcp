// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package cluster

import (
	"bytes"
	"github.com/kislovrs/godhcp/internal/storage"
	"github.com/stretchr/testify/require"
	"io"
	"strings"
	"testing"
	"time"
)

func TestDNSIntentSnapshotAndCAS(t *testing.T) {
	s := NewLeaseState()
	owner := strings.Repeat("ab", 32)
	require.Equal(t, owner, s.applyDNS(DNSCommand{Owner: owner}))
	r := storage.DNSRecord{Server: "192.0.2.53", Zone: "example.test", Name: "host.example.test", Type: "A", Value: "192.0.2.100", Token: owner, TTL: 300, LeaseExpires: time.Now().UTC().Add(time.Hour)}
	require.Nil(t, s.applyDNS(DNSCommand{Key: r.Key(), After: &r}))
	applied := r
	applied.AppliedAt = time.Now().UTC()
	require.Nil(t, s.applyDNS(DNSCommand{Key: r.Key(), Before: &r, After: &applied}))
	require.NotNil(t, s.applyDNS(DNSCommand{Key: r.Key(), Before: &r}))
	snap, e := s.Snapshot()
	require.NoError(t, e)
	restored := NewLeaseState()
	require.NoError(t, restored.Restore(io.NopCloser(bytes.NewReader(snap.(leaseSnapshot)))))
	require.Equal(t, owner, restored.dnsOwner)
	require.Equal(t, applied, restored.dns[r.Key()])
	require.Nil(t, restored.applyDNS(DNSCommand{Key: r.Key(), Before: &applied}))
	require.Empty(t, restored.dns)
}
