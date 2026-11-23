// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package cluster

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"github.com/kislovrs/godhcp/internal/config"
	"github.com/kislovrs/godhcp/internal/storage"
	"io"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type lostPairAck struct{ http.RoundTripper }

func (t lostPairAck) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.RoundTripper.RoundTrip(r)
	if err == nil && r.Method == http.MethodPut {
		resp.Body.Close()
		return nil, errors.New("lost replication acknowledgement")
	}
	return resp, err
}

type stalledPairProbe struct{ started, release chan struct{} }

func (t stalledPairProbe) RoundTrip(r *http.Request) (*http.Response, error) {
	close(t.started)
	select {
	case <-t.release:
	case <-r.Context().Done():
	}
	return nil, errors.New("peer offline")
}

func testPair(t *testing.T, configs []*tls.Config, identities []string) {
	ctx := context.Background()
	listeners := make([]net.Listener, 2)
	peers := make([]Peer, 2)
	dirs := []string{t.TempDir(), t.TempDir()}
	nodes := make([]*Node, 2)
	for i := range listeners {
		l, e := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, e)
		listeners[i] = l
		id := string(rune('a' + i))
		peers[i] = Peer{id, l.Addr().String(), id + ".test", identities[i]}
	}
	fences := []int{0, 0}
	failed := false
	open := func(i int, l net.Listener) *Node {
		n, e := OpenPair(peers[i].ID, "a", dirs[i], l, configs[i], peers, NewLeaseState(), 30*time.Second, func(context.Context, string) error {
			fences[i]++
			if failed {
				return errors.New("BMC unavailable")
			}
			return nil
		})
		require.NoError(t, e)
		t.Cleanup(n.Close)
		return n
	}
	for i := range nodes {
		nodes[i] = open(i, listeners[i])
	}
	active, e := nodes[1].PairStep(ctx)
	require.NoError(t, e)
	require.False(t, active)
	active, e = nodes[0].PairStep(ctx)
	require.NoError(t, e)
	require.True(t, active)
	require.Equal(t, []int{0, 0}, fences)
	// A CA-authorized certificate outside the exact pair cannot read its state.
	outsider := &http.Client{Transport: &http.Transport{TLSClientConfig: PeerTLS(configs[2], peers[1].Hostname, peers[1].Identity)}, Timeout: time.Second}
	response, e := outsider.Get("https://" + peers[1].Address + "/v1/state")
	require.NoError(t, e)
	require.Equal(t, 403, response.StatusCode)
	response.Body.Close()
	outsider.CloseIdleConnections()
	// Durable DUID/config/lease code paths use the same transaction adapter.
	_, e = nodes[0].commitResult(ctx, LeaseCommand{Version: 1, DUID: []byte{0, 4, 1, 2}})
	require.NoError(t, e)
	require.Equal(t, nodes[0].pair.image, nodes[1].pair.image)
	badVersion := nodes[0].pair.image
	badVersion.Version++
	_, e = nodes[0].pair.request(ctx, http.MethodPut, &badVersion)
	require.Error(t, e, "identical heartbeat must still reject unsupported envelope version")
	stale := nodes[0].pair.image
	stale.Index--
	_, e = nodes[0].pair.request(ctx, http.MethodPut, &stale)
	require.Error(t, e)
	corrupt := nodes[0].pair.image
	corrupt.Data = json.RawMessage(bytes.ReplaceAll(corrupt.Data, []byte(`"Version":1`), []byte(`"Version":9`)))
	_, e = nodes[0].pair.request(ctx, http.MethodPut, &corrupt)
	require.Error(t, e)
	require.Equal(t, nodes[0].pair.image, nodes[1].pair.image)
	store := &LeaseStore{Node: nodes[0], State: nodes[0].pair.state}
	cfg, e := config.Load("../../configs/example.yml")
	require.NoError(t, e)
	cfg.Subnets[0].Domain = "pair.example"
	revision, e := store.CommitConfiguration(ctx, 0, cfg)
	require.NoError(t, e)
	require.Equal(t, uint64(1), revision)
	request6 := storage.IPv6Request{Action: storage.Bind6, Prefix: "fd42:42::/64", Client: "pair-client", IAID: 1, Start: netip.MustParseAddr("fd42:42::10"), End: netip.MustParseAddr("fd42:42::20"), Now: time.Now().UTC(), Lifetime: time.Hour}
	ip6, e := store.ApplyIPv6(ctx, request6)
	require.NoError(t, e)
	owner, e := store.InitDNS(ctx)
	require.NoError(t, e)
	intent := storage.DNSRecord{Server: "192.0.2.53", Zone: "example.test", Name: "pair.example.test", Type: "A", Value: "192.0.2.100", Token: owner, TTL: 300, LeaseExpires: time.Now().UTC().Add(time.Hour)}
	require.NoError(t, store.PutDNSRecord(ctx, intent))
	// Crash after local persistence but before mirroring: the old primary has
	// one unacknowledged index, equal to the backup's next ownership transition.
	pending := NewLeaseState()
	require.NoError(t, pending.Restore(io.NopCloser(bytes.NewReader(nodes[0].pair.image.Data))))
	pending.index++
	pendingSnapshot, e := pending.Snapshot()
	require.NoError(t, e)
	require.NoError(t, nodes[0].pair.save(pairImage{1, pending.index, "a", json.RawMessage(pendingSnapshot.(leaseSnapshot))}))
	// A stopped primary's last acknowledged state survives promotion.
	nodes[0].Close()
	nodes[1].pair.last = time.Now().Add(-time.Minute)
	active, e = nodes[1].PairStep(ctx)
	require.NoError(t, e)
	require.True(t, active)
	require.Equal(t, 1, fences[1])
	// An offline-peer probe must not serialize DHCP commits behind its timeout.
	originalTransport := nodes[1].pair.client.Transport
	stalled := stalledPairProbe{make(chan struct{}), make(chan struct{})}
	nodes[1].pair.client.Transport = stalled
	stepped := make(chan error, 1)
	go func() { _, err := nodes[1].PairStep(ctx); stepped <- err }()
	<-stalled.started
	committed := make(chan error, 1)
	go func() {
		_, err := nodes[1].commitResult(ctx, LeaseCommand{Version: 1, DUID: []byte{0, 4, 1, 2}})
		committed <- err
	}()
	fast := false
	select {
	case err := <-committed:
		require.NoError(t, err)
		fast = true
	case <-time.After(time.Second):
	}
	close(stalled.release)
	require.NoError(t, <-stepped)
	require.True(t, fast, "offline peer blocked DHCP commit")
	nodes[1].pair.client.Transport = originalTransport

	store = &LeaseStore{Node: nodes[1], State: nodes[1].pair.state}
	restored, _, e := store.LoadConfiguration(ctx, cfg)
	require.NoError(t, e)
	require.Equal(t, "pair.example", restored.Subnets[0].Domain)
	request6.Action = storage.Renew6
	request6.Now = request6.Now.Add(time.Minute)
	renewed, e := store.ApplyIPv6(ctx, request6)
	require.NoError(t, e)
	require.Equal(t, ip6, renewed)
	savedOwner, e := store.InitDNS(ctx)
	require.NoError(t, e)
	require.Equal(t, owner, savedOwner)
	records, e := store.DNSRecords(ctx)
	require.NoError(t, e)
	require.Equal(t, []storage.DNSRecord{intent}, records)

	_, e = nodes[1].commitResult(ctx, LeaseCommand{Version: 1, DUID: []byte{0, 4, 1, 2}})
	require.NoError(t, e)
	// Returning original primary cannot reclaim service with its stale disk.
	l, e := net.Listen("tcp", peers[0].Address)
	require.NoError(t, e)
	nodes[0] = open(0, l)
	require.False(t, nodes[0].pair.armed)
	require.Error(t, nodes[0].CheckAuthority(ctx))
	active, e = nodes[0].PairStep(ctx)
	require.NoError(t, e)
	require.False(t, active)
	// Primary polling and active replication must not deadlock each other's
	// receiver locks and spuriously fence a healthy returning node.
	polling, stopPolling := context.WithCancel(ctx)
	polled := make(chan struct{})
	go func() {
		defer close(polled)
		for polling.Err() == nil {
			nodes[0].PairStep(polling)
		}
	}()
	for range 10 {
		_, e = nodes[1].PairStep(ctx)
		require.NoError(t, e)
	}
	stopPolling()
	<-polled
	require.Equal(t, 1, fences[1], "healthy returning primary was fenced")
	require.True(t, nodes[0].pair.armed)
	require.False(t, nodes[1].pair.solo)
	require.Equal(t, nodes[1].pair.image, nodes[0].pair.image)
	// Restarting both members requires contact and picks the freshest disk.
	nodes[0].Close()
	nodes[1].Close()
	for i := range nodes {
		l, e = net.Listen("tcp", peers[i].Address)
		require.NoError(t, e)
		nodes[i] = open(i, l)
		require.False(t, nodes[i].pair.armed)
	}
	active, e = nodes[1].PairStep(ctx)
	require.NoError(t, e)
	require.False(t, active)
	active, e = nodes[0].PairStep(ctx)
	require.NoError(t, e)
	require.True(t, active)
	// Failed fencing cannot authorize a local-only ACK and permanently blocks this process.
	nodes[1].Close()
	failed = true
	_, e = nodes[0].commitResult(ctx, LeaseCommand{Version: 1, DUID: []byte{0, 4, 1, 2}})
	require.Error(t, e)
	require.Error(t, nodes[0].CheckAuthority(ctx))
	_, e = nodes[0].PairStep(ctx)
	require.Error(t, e)
	// Repeat from restarted processes and lose the rejoin acknowledgement. The
	// returning backup has armed itself, so the active must fence it again.
	nodes[0].Close()
	failed = false
	for i := range nodes {
		l, e = net.Listen("tcp", peers[i].Address)
		require.NoError(t, e)
		nodes[i] = open(i, l)
	}
	active, e = nodes[0].PairStep(ctx)
	require.NoError(t, e)
	require.True(t, active)
	nodes[1].Close()
	_, e = nodes[0].PairStep(ctx)
	require.NoError(t, e)
	require.True(t, nodes[0].pair.solo)
	l, e = net.Listen("tcp", peers[1].Address)
	require.NoError(t, e)
	nodes[1] = open(1, l)
	nodes[0].pair.client.Transport = lostPairAck{nodes[0].pair.client.Transport}
	failed = true
	_, e = nodes[0].PairStep(ctx)
	require.Error(t, e)
	require.True(t, nodes[1].pair.armed)
	require.Error(t, nodes[0].CheckAuthority(ctx))

}
