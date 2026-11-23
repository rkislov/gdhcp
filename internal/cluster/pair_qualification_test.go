// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package cluster

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/kislovrs/godhcp/internal/model"
	"github.com/stretchr/testify/require"
)

// Opt-in capacity/failover qualification uses real mTLS and fsync databases.
func qualifyPair(t *testing.T, security []*tls.Config, identities []string) {
	if os.Getenv("GODHCP_PAIR_QUALIFY") != "1" {
		t.Skip("set GODHCP_PAIR_QUALIFY=1")
	}
	for _, size := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			ctx := context.Background()
			nodes := make([]*Node, 2)
			peers := make([]Peer, 2)
			listeners := make([]net.Listener, 2)
			dirs := []string{t.TempDir(), t.TempDir()}
			for i := range nodes {
				l, e := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, e)
				listeners[i] = l
				id := string(rune('a' + i))
				peers[i] = Peer{id, l.Addr().String(), id + ".test", identities[i]}
			}
			fences := 0
			open := func(i int, l net.Listener) *Node {
				n, e := OpenPair(peers[i].ID, "a", dirs[i], l, security[i], peers, NewLeaseState(), 30*time.Second, func(context.Context, string) error { fences++; return fmt.Errorf("unexpected fencing in healthy pair") })
				require.NoError(t, e)
				t.Cleanup(n.Close)
				return n
			}
			for i := range nodes {
				nodes[i] = open(i, listeners[i])
			}
			active, e := nodes[0].PairStep(ctx)
			require.NoError(t, e)
			require.True(t, active)
			now := time.Now().UTC()
			expected := make(map[string]model.Lease, size)
			for begin := 0; begin < size; begin += 500 {
				changes := []LeaseChange{}
				for i := begin; i < begin+500 && i < size; i++ {
					address := fmt.Sprintf("10.%d.%d.%d", i/65536, (i/256)%256, i%256)
					l := model.Lease{IP: address, MAC: fmt.Sprintf("02:00:00:%02x:%02x:%02x", i>>16, i>>8&255, i&255), SubnetID: "load", State: model.StateBound, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
					expected[address] = l
					changes = append(changes, LeaseChange{IP: address, After: &l})
				}
				require.NoError(t, nodes[0].CommitLeases(changes, 20*time.Second))
			}
			elapsed := make([]time.Duration, 100)
			for i := range elapsed {
				address := fmt.Sprintf("10.0.%d.%d", (i/256)%256, i%256)
				before := expected[address]
				after := before
				after.ExpiresAt = after.ExpiresAt.Add(time.Minute)
				started := time.Now()
				require.NoError(t, nodes[0].CommitLeases([]LeaseChange{{IP: address, Before: &before, After: &after}}, 20*time.Second))
				elapsed[i] = time.Since(started)
				expected[address] = after
			}
			require.Equal(t, 0, fences)
			// Both disks must survive reopening; compare every acknowledged binding.
			for _, n := range nodes {
				n.Close()
			}
			for i := range nodes {
				l, e := net.Listen("tcp", peers[i].Address)
				require.NoError(t, e)
				nodes[i] = open(i, l)
				for address, want := range expected {
					got, ok := nodes[i].pair.state.Get(address)
					require.True(t, ok)
					require.Equal(t, want, got)
				}
			}
			active, e = nodes[0].PairStep(ctx)
			require.NoError(t, e)
			require.True(t, active)
			sort.Slice(elapsed, func(i, j int) bool { return elapsed[i] < elapsed[j] })
			t.Logf("CAPACITY leases=%d snapshot_bytes=%d commits=100 p50=%s p95=%s max=%s acknowledged_leases_recovered=%d", size, len(nodes[0].pair.image.Data), elapsed[50], elapsed[95], elapsed[99], len(expected))
		})
	}
}
