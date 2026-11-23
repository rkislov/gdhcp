// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package cluster

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/kislovrs/godhcp/internal/model"
	"github.com/stretchr/testify/require"
)

func pairFaultFixture(t *testing.T, security []*tls.Config, ids []string) ([]*Node, func(int), func(int), *int) {
	t.Helper()
	nodes := make([]*Node, 2)
	peers := make([]Peer, 2)
	dirs := []string{t.TempDir(), t.TempDir()}
	listeners := make([]net.Listener, 2)
	count := 0
	for i := range nodes {
		l, e := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, e)
		listeners[i] = l
		name := string(rune('a' + i))
		peers[i] = Peer{name, l.Addr().String(), name + ".test", ids[i]}
	}
	open := func(i int) {
		l := listeners[i]
		if nodes[i] != nil {
			var e error
			l, e = net.Listen("tcp", peers[i].Address)
			require.NoError(t, e)
		}
		n, e := OpenPair(peers[i].ID, "a", dirs[i], l, security[i], peers, NewLeaseState(), 30*time.Second, func(context.Context, string) error { count++; return errors.New("fencing denied by fixture") })
		require.NoError(t, e)
		nodes[i] = n
		t.Cleanup(n.Close)
	}
	closeNode := func(i int) { nodes[i].Close() }
	open(0)
	open(1)
	return nodes, open, closeNode, &count
}

// Drop only the first successful PUT response, after the receiver has fsynced.
type firstPairAckLost struct {
	http.RoundTripper
	lost bool
}

func (tr *firstPairAckLost) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := tr.RoundTripper.RoundTrip(r)
	if err == nil && r.Method == http.MethodPut && !tr.lost {
		tr.lost = true
		resp.Body.Close()
		return nil, errors.New("first replication acknowledgement lost")
	}
	return resp, err
}
func testPairFaults(t *testing.T, security []*tls.Config, ids []string) {
	ctx := context.Background()
	t.Run("failed startup acknowledgement cannot advance local history", func(t *testing.T) {
		nodes, _, _, _ := pairFaultFixture(t, security, ids)
		nodes[0].pair.client.Transport = lostPairAck{nodes[0].pair.client.Transport}
		for range 20 {
			active, e := nodes[0].PairStep(ctx)
			require.NoError(t, e)
			require.False(t, active)
			require.Greater(t, nodes[1].pair.image.Index, nodes[0].pair.image.Index)
		}
		require.Error(t, nodes[0].CheckAuthority(ctx))
	})
	for _, where := range []int{0, 1} {
		t.Run(fmt.Sprintf("disk failure node %d", where), func(t *testing.T) {
			nodes, _, _, fences := pairFaultFixture(t, security, ids)
			active, e := nodes[0].PairStep(ctx)
			require.NoError(t, e)
			require.True(t, active)
			before := nodes[0].pair.image.Index
			prior, err := nodes[where].pair.state.Snapshot()
			require.NoError(t, err)
			require.NoError(t, nodes[where].pair.disk.Close())
			_, e = nodes[0].commitResult(ctx, LeaseCommand{Version: 1, DUID: []byte{0, 4, 1, 2}})
			require.Error(t, e)
			require.Error(t, nodes[0].CheckAuthority(ctx))
			after, err := nodes[where].pair.state.Snapshot()
			require.NoError(t, err)
			require.Equal(t, prior, after, "failed fsync must not publish prepared state")
			if where == 0 {
				require.Equal(t, before, nodes[1].pair.image.Index)
				require.Equal(t, 0, *fences)
			} else {
				require.Equal(t, 1, *fences)
			}
		})
	}
	t.Run("single lost acknowledgement retries identical durable state", func(t *testing.T) {
		nodes, _, _, fences := pairFaultFixture(t, security, ids)
		active, err := nodes[0].PairStep(ctx)
		require.NoError(t, err)
		require.True(t, active)
		before := nodes[0].pair.image.Index
		nodes[0].pair.client.Transport = &firstPairAckLost{RoundTripper: nodes[0].pair.client.Transport}
		_, err = nodes[0].commitResult(ctx, LeaseCommand{Version: 1, DUID: []byte{0, 4, 1, 2}})
		require.NoError(t, err)
		require.Equal(t, before+1, nodes[0].pair.image.Index, "retry must not apply mutation twice")
		require.Equal(t, nodes[0].pair.image, nodes[1].pair.image)
		require.Zero(t, *fences)
		require.False(t, nodes[0].pair.solo)
	})
	t.Run("ack loss after mutation fences before any success", func(t *testing.T) {
		nodes, _, _, fences := pairFaultFixture(t, security, ids)
		active, e := nodes[0].PairStep(ctx)
		require.NoError(t, e)
		require.True(t, active)
		nodes[0].pair.client.Transport = lostPairAck{nodes[0].pair.client.Transport}
		_, e = nodes[0].commitResult(ctx, LeaseCommand{Version: 1, DUID: []byte{0, 4, 1, 2}})
		require.Error(t, e)
		require.Equal(t, nodes[0].pair.image, nodes[1].pair.image)
		require.Equal(t, 1, *fences)
		require.Error(t, nodes[0].CheckAuthority(ctx))
	})
	t.Run("twenty alternating failovers retain every acknowledged lease", func(t *testing.T) {
		nodes, reopen, closeNode, _ := pairFaultFixture(t, security, ids)
		active, e := nodes[0].PairStep(ctx)
		require.NoError(t, e)
		require.True(t, active)
		expected := map[string]model.Lease{}
		owner := 0
		for round := range 20 {
			now := time.Now().UTC()
			address := fmt.Sprintf("192.0.2.%d", 100+round)
			lease := model.Lease{IP: address, MAC: fmt.Sprintf("02:00:00:00:01:%02x", round), SubnetID: "test", State: model.StateBound, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
			require.NoError(t, nodes[owner].CommitLeases([]LeaseChange{{IP: address, After: &lease}}, 5*time.Second))
			expected[address] = lease
			previous := owner
			owner = 1 - owner
			closeNode(previous)
			// Successful fencing is permitted only after confirmed shutdown of target.
			nodes[owner].pair.fence = func(context.Context, string) error { require.True(t, nodes[previous].pair.closed); return nil }
			nodes[owner].pair.last = time.Now().Add(-time.Minute)
			active, e = nodes[owner].PairStep(ctx)
			require.NoError(t, e)
			require.True(t, active)
			for address, want := range expected {
				got, ok := nodes[owner].pair.state.Get(address)
				require.True(t, ok)
				require.Equal(t, want, got)
			}
			reopen(previous)
			_, e = nodes[owner].PairStep(ctx)
			require.NoError(t, e)
			require.True(t, nodes[previous].pair.armed)
			require.False(t, nodes[owner].pair.solo)
		}
		t.Log("20 failovers, all 20 acknowledged leases retained on both nodes")
	})
	t.Run("expired request cannot write", func(t *testing.T) {
		nodes, _, _, _ := pairFaultFixture(t, security, ids)
		active, e := nodes[0].PairStep(ctx)
		require.NoError(t, e)
		require.True(t, active)
		before := nodes[0].pair.image.Index
		c, cancel := context.WithCancel(ctx)
		cancel()
		_, e = nodes[0].commitResult(c, LeaseCommand{Version: 1, DUID: []byte{0, 4, 1, 2}})
		require.Error(t, e)
		require.Equal(t, before, nodes[0].pair.image.Index)
		_, e = nodes[0].pair.request(c, http.MethodGet, nil)
		require.Error(t, e)
	})
}
