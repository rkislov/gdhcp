// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package cluster

import (
	"bytes"
	"github.com/kislovrs/godhcp/internal/storage"
	"github.com/stretchr/testify/require"
	"io"
	"net/netip"
	"testing"
	"time"
)

func TestIPv6QuarantineAndSnapshot(t *testing.T) {
	s := NewLeaseState()
	s.duid = []byte{0, 4, 1}
	r := storage.IPv6Request{Action: storage.Bind6, Prefix: "fd42::/64", Client: "a", IAID: 7, Start: netip.MustParseAddr("fd42::10"), End: netip.MustParseAddr("fd42::10"), Now: time.Now().UTC(), Lifetime: time.Hour}
	require.Equal(t, r.Start, s.apply6(r))
	r.Action = storage.Decline6
	r.Hints = []netip.Addr{r.Start}
	require.Equal(t, r.Start, s.apply6(r))
	snap, e := s.Snapshot()
	require.NoError(t, e)
	restored := NewLeaseState()
	require.NoError(t, restored.Restore(io.NopCloser(bytes.NewReader(snap.(leaseSnapshot)))))
	require.Equal(t, s.duid, restored.duid)
	r.Action = storage.Bind6
	r.Client = "b"
	r.Now = r.Now.Add(time.Minute)
	require.ErrorIs(t, restored.apply6(r).(error), storage.ErrNoAddress6)
	r.Now = r.Now.Add(time.Hour)
	require.Equal(t, r.Start, restored.apply6(r))
	r.Action = storage.Release6
	r.Client = "a"
	require.ErrorIs(t, restored.apply6(r).(error), storage.ErrNoBinding6)
	r.Client = "b"
	require.Equal(t, r.Start, restored.apply6(r))
	r.Action = storage.Bind6
	r.Client = "c"
	require.Equal(t, r.Start, restored.apply6(r))
}
