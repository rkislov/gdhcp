// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package cluster

import (
	"crypto/tls"
	"encoding/json"
	"github.com/hashicorp/raft"
	"github.com/stretchr/testify/require"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type testState struct {
	mu     sync.Mutex
	Values []string
}

func (s *testState) Apply(l *raft.Log) interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Values = append(s.Values, string(l.Data))
	return nil
}
func (s *testState) Snapshot() (raft.FSMSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, e := json.Marshal(s.Values)
	return testSnapshot(b), e
}
func (s *testState) Restore(r io.ReadCloser) error {
	defer r.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	return json.NewDecoder(r).Decode(&s.Values)
}
func (s *testState) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.Values) }

type testSnapshot []byte

func (s testSnapshot) Persist(sink raft.SnapshotSink) error {
	if _, e := sink.Write(s); e != nil {
		sink.Cancel()
		return e
	}
	return sink.Close()
}
func (testSnapshot) Release() {}

func testConsensus(t *testing.T, configs []*tls.Config, identities []string) {
	listeners := make([]net.Listener, 3)
	peers := make([]Peer, 3)
	nodes := make([]*Node, 3)
	states := make([]*testState, 3)
	dirs := make([]string, 3)
	for i := range listeners {
		l, e := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, e)
		listeners[i] = l
		t.Cleanup(func() { l.Close() })
		id := string(rune('a' + i))
		peers[i] = Peer{id, l.Addr().String(), id + ".test", identities[i]}
	}
	for i := range nodes {
		dirs[i] = t.TempDir()
		states[i] = &testState{}
		n, e := Open(peers[i].ID, dirs[i], listeners[i], configs[i], peers, i == 0, states[i])
		require.NoError(t, e)
		nodes[i] = n
		t.Cleanup(n.Close)
	}
	leader := -1
	require.Eventually(t, func() bool {
		for i, n := range nodes {
			if n.Raft.State() == raft.Leader {
				leader = i
				return true
			}
		}
		return false
	}, 10*time.Second, 20*time.Millisecond)
	require.NoError(t, nodes[leader].Commit([]byte("lease-one"), 3*time.Second))
	require.Eventually(t, func() bool {
		for _, s := range states {
			if s.count() != 1 {
				return false
			}
		}
		return true
	}, 5*time.Second, 20*time.Millisecond)
	old := leader
	nodes[old].Close()
	require.Eventually(t, func() bool {
		for i, n := range nodes {
			if i != old && n.Raft.State() == raft.Leader {
				leader = i
				return true
			}
		}
		return false
	}, 10*time.Second, 20*time.Millisecond)
	require.NoError(t, nodes[leader].Commit([]byte("lease-two"), 3*time.Second))
	// Reopen the former leader's durable store and catch up over mTLS.
	l, e := net.Listen("tcp", peers[old].Address)
	require.NoError(t, e)
	states[old] = &testState{}
	n, e := Open(peers[old].ID, dirs[old], l, configs[old], peers, false, states[old])
	require.NoError(t, e)
	nodes[old] = n
	t.Cleanup(n.Close)
	require.Eventually(t, func() bool { return states[old].count() == 2 }, 10*time.Second, 20*time.Millisecond)
	// One remaining voter must not acknowledge new mutations.
	for i, n := range nodes {
		if i != leader {
			n.Close()
		}
	}
	require.Error(t, nodes[leader].Commit([]byte("no-majority"), 2*time.Second))
	require.Equal(t, 2, states[leader].count())
}
