// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package cluster

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"github.com/hashicorp/raft"
	"github.com/kislovrs/godhcp/internal/api"
	"github.com/kislovrs/godhcp/internal/config"
	"github.com/kislovrs/godhcp/internal/core"
	"github.com/kislovrs/godhcp/internal/dhcp"
	"github.com/kislovrs/godhcp/internal/logbuf"
	"github.com/kislovrs/godhcp/internal/metrics"
	"github.com/kislovrs/godhcp/internal/model"
	"github.com/kislovrs/godhcp/internal/storage"
	"github.com/stretchr/testify/require"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"testing"
	"time"
)

func TestLeaseAtomicityAndRestore(t *testing.T) {
	s := NewLeaseState()
	now := time.Now().UTC()
	a := model.Lease{IP: "192.0.2.10", MAC: "02:00:00:00:00:01", State: model.StateBound, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	apply := func(index uint64, changes ...LeaseChange) interface{} {
		b, e := json.Marshal(LeaseCommand{Version: 1, Changes: changes})
		require.NoError(t, e)
		return s.Apply(&raft.Log{Index: index, Data: b})
	}
	require.Nil(t, apply(1, LeaseChange{IP: a.IP, After: &a}))
	require.Nil(t, apply(2, LeaseChange{IP: a.IP, After: &a}))
	other := a
	other.MAC = "02:00:00:00:00:02"
	fresh := a
	fresh.IP = "192.0.2.11"
	require.Error(t, apply(3, LeaseChange{IP: fresh.IP, After: &fresh}, LeaseChange{IP: a.IP, After: &other}).(error))
	_, ok := s.Get(fresh.IP)
	require.False(t, ok)
	got, _ := s.Get(a.IP)
	require.Equal(t, a.MAC, got.MAC)
	snap, e := s.Snapshot()
	require.NoError(t, e)
	restored := NewLeaseState()
	require.NoError(t, restored.Restore(io.NopCloser(bytes.NewReader(snap.(leaseSnapshot)))))
	got, ok = restored.Get(a.IP)
	require.True(t, ok)
	require.Equal(t, a, got)
	require.Error(t, restored.Restore(io.NopCloser(bytes.NewBufferString(`{"Version":2}`))))
	got, ok = restored.Get(a.IP)
	require.True(t, ok)
	require.Equal(t, a, got)
	require.Nil(t, apply(4, LeaseChange{IP: a.IP, Before: &a}))
	require.Error(t, apply(5, LeaseChange{IP: a.IP, Before: &a, After: &other}).(error))
}

func testLeaseFailover(t *testing.T, configs []*tls.Config, identities []string) {
	peers := make([]Peer, 3)
	ls := make([]net.Listener, 3)
	nodes := make([]*Node, 3)
	states := make([]*LeaseState, 3)
	dirs := make([]string, 3)
	for i := range ls {
		l, e := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, e)
		ls[i] = l
		t.Cleanup(func() { l.Close() })
		id := string(rune('a' + i))
		peers[i] = Peer{id, l.Addr().String(), id + ".test", identities[i]}
	}
	for i := range nodes {
		dirs[i] = t.TempDir()
		states[i] = NewLeaseState()
		n, e := Open(peers[i].ID, dirs[i], ls[i], configs[i], peers, i == 0, states[i])
		require.NoError(t, e)
		nodes[i] = n
		t.Cleanup(n.Close)
	}
	leader := -1
	find := func(exclude int) bool {
		for i, n := range nodes {
			if i != exclude && n.Raft.State() == raft.Leader {
				leader = i
				return true
			}
		}
		return false
	}
	require.Eventually(t, func() bool { return find(-1) }, 10*time.Second, 20*time.Millisecond)
	now := time.Now().UTC()
	l := model.Lease{IP: "192.0.2.20", MAC: "02:00:00:00:00:01", State: model.StateBound, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	require.NoError(t, nodes[leader].CommitLeases([]LeaseChange{{IP: l.IP, After: &l}}, 3*time.Second))
	ctx := context.Background()
	makeService := func(i int) *core.Service {
		cfg, e := config.Load("../../configs/example.yml")
		require.NoError(t, e)
		if image := states[i].Configuration(); image.Revision > 0 {
			cfg, e = MergeConfiguration(image, cfg)
			require.NoError(t, e)
		}
		catalog, e := storage.Open(ctx, "sqlite", ":memory:")
		require.NoError(t, e)
		t.Cleanup(func() { catalog.Close() })
		svc, e := core.New(ctx, core.Options{Config: cfg, Store: &LeaseStore{Store: catalog, Node: nodes[i], State: states[i]}})
		require.NoError(t, e)
		return svc
	}
	policyStore := &LeaseStore{Node: nodes[leader], State: states[leader]}
	sharedCfg, e := config.Load("../../configs/example.yml")
	require.NoError(t, e)
	sharedCfg.Subnets[0].Domain = "cluster.example"
	sharedCfg.API.Auth.APIKeys = []config.APIKey{{Name: "cluster-test", Key: "cluster-test-key", Role: "admin"}}
	policyRevision, e := policyStore.CommitConfiguration(ctx, 0, sharedCfg)
	require.NoError(t, e)
	require.Equal(t, uint64(1), policyRevision)
	_, e = policyStore.CommitConfiguration(ctx, 0, sharedCfg)
	require.Error(t, e)
	hooks := &fakeActivation{}
	require.NoError(t, nodes[leader].Takeover(ctx, peers[leader].ID, states[leader], hooks))
	require.Equal(t, []string{"stop", "ip-down", "ip-up", "start"}, hooks.Events)
	svc := makeService(leader)
	handler := api.New(svc, metrics.New(), logbuf.New(50)).Handler()
	localBefore, e := os.ReadFile("../../configs/example.yml")
	require.NoError(t, e)
	proposed, e := cloneConfiguration(svc.Config())
	require.NoError(t, e)
	proposed.Subnets[0].Domain = "web.example"
	payload, e := json.Marshal(proposed)
	require.NoError(t, e)
	put := func(data []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(data))
		req.Header.Set("X-API-Key", "cluster-test-key")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	response := put(payload)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = put(payload)
	require.Equal(t, 400, response.Code, response.Body.String())
	require.NoError(t, svc.ReloadConfiguration(ctx))
	require.Equal(t, "web.example", svc.Config().Subnets[0].Domain)
	localAfter, e := os.ReadFile("../../configs/example.yml")
	require.NoError(t, e)
	require.Equal(t, localBefore, localAfter)

	packet := &dhcp.Packet{Op: dhcp.BootRequest, HType: 1, HLen: 6, XID: 123}
	packet.CHAddr = net.HardwareAddr{2, 0, 0, 0, 0, 42}
	packet.Set(dhcp.OptMessageType, []byte{dhcp.MsgDiscover})
	offer, e := svc.Handle(ctx, core.Input{Packet: packet, Iface: "eth0.10"})
	require.NoError(t, e)
	require.NotNil(t, offer)
	require.Equal(t, dhcp.MsgOffer, offer.Packet.MessageType())
	allocated := offer.Packet.YIAddr
	packet.Set(dhcp.OptMessageType, []byte{dhcp.MsgRequest})
	address := allocated.As4()
	packet.Set(dhcp.OptRequestedIP, address[:])
	ack, e := svc.Handle(ctx, core.Input{Packet: packet, Iface: "eth0.10"})
	require.NoError(t, e)
	require.NotNil(t, ack)
	require.Equal(t, dhcp.MsgAck, ack.Packet.MessageType())
	v6store := &LeaseStore{Node: nodes[leader], State: states[leader]}
	duid, e := v6store.InitIPv6(ctx, nil)
	require.NoError(t, e)
	v6request := storage.IPv6Request{Action: storage.Bind6, Prefix: "fd42:42::/64", Client: "client6", IAID: 1, Start: netip.MustParseAddr("fd42:42::10"), End: netip.MustParseAddr("fd42:42::11"), Now: time.Now().UTC(), Lifetime: time.Hour}
	v6ip, e := v6store.ApplyIPv6(ctx, v6request)
	require.NoError(t, e)
	dnsOwner, e := v6store.InitDNS(ctx)
	require.NoError(t, e)
	intent := storage.DNSRecord{Server: "192.0.2.53", Zone: "example.test", Name: "client.example.test", Type: "A", Value: allocated.String(), Token: dnsOwner, TTL: 300, LeaseExpires: time.Now().UTC().Add(time.Hour)}
	require.NoError(t, v6store.PutDNSRecord(ctx, intent))
	old := leader
	nodes[old].Close()
	require.Eventually(t, func() bool { return find(old) }, 10*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool { got, ok := states[leader].Get(l.IP); return ok && got.MAC == l.MAC }, 5*time.Second, 20*time.Millisecond)
	failedHooks := &fakeActivation{Failure: "fence:" + peers[old].ID}
	require.Error(t, nodes[leader].Takeover(ctx, peers[leader].ID, states[leader], failedHooks))
	require.NotContains(t, failedHooks.Events, "ip-up")
	require.NotContains(t, failedHooks.Events, "start")
	hooks = &fakeActivation{}
	require.NoError(t, nodes[leader].Takeover(ctx, peers[leader].ID, states[leader], hooks))
	require.Contains(t, hooks.Events, "fence:"+peers[old].ID)
	require.Equal(t, "start", hooks.Events[len(hooks.Events)-1])
	v6store = &LeaseStore{Node: nodes[leader], State: states[leader]}
	savedOwner, e := v6store.InitDNS(ctx)
	require.NoError(t, e)
	require.Equal(t, dnsOwner, savedOwner)
	intents, e := v6store.DNSRecords(ctx)
	require.NoError(t, e)
	require.Equal(t, []storage.DNSRecord{intent}, intents)
	savedDUID, e := v6store.InitIPv6(ctx, nil)
	require.NoError(t, e)
	require.Equal(t, duid, savedDUID)
	v6request.Action = storage.Renew6
	v6request.Now = v6request.Now.Add(time.Minute)
	renewed6, e := v6store.ApplyIPv6(ctx, v6request)
	require.NoError(t, e)
	require.Equal(t, v6ip, renewed6)
	svc = makeService(leader)
	require.Equal(t, "web.example", svc.Config().Subnets[0].Domain)
	packet.CIAddr = allocated
	ack, e = svc.Handle(ctx, core.Input{Packet: packet, Iface: "eth0.10"})
	require.NoError(t, e)
	require.NotNil(t, ack)
	require.Equal(t, dhcp.MsgAck, ack.Packet.MessageType())
	require.Equal(t, allocated, ack.Packet.YIAddr)
	// A new leader cannot overwrite an existing lease with another owner.
	stolen := l
	stolen.MAC = "02:00:00:00:00:02"
	require.Error(t, nodes[leader].CommitLeases([]LeaseChange{{IP: l.IP, After: &stolen}}, 3*time.Second))
	renewed := l
	renewed.ExpiresAt = now.Add(2 * time.Hour)
	require.NoError(t, nodes[leader].CommitLeases([]LeaseChange{{IP: l.IP, Before: &l, After: &renewed}}, 3*time.Second))
	require.NoError(t, nodes[leader].Raft.Snapshot().Error())
	listener, e := net.Listen("tcp", peers[old].Address)
	require.NoError(t, e)
	states[old] = NewLeaseState()
	n, e := Open(peers[old].ID, dirs[old], listener, configs[old], peers, false, states[old])
	require.NoError(t, e)
	nodes[old] = n
	t.Cleanup(n.Close)
	require.Eventually(t, func() bool { got, ok := states[old].Get(l.IP); return ok && got.ExpiresAt.Equal(renewed.ExpiresAt) }, 10*time.Second, 20*time.Millisecond)
	for i, n := range nodes {
		if i != leader {
			n.Close()
		}
	}
	require.Error(t, nodes[leader].CommitLeases([]LeaseChange{{IP: l.IP, Before: &renewed}}, 2*time.Second))
	got, ok := states[leader].Get(l.IP)
	require.True(t, ok)
	require.Equal(t, l.MAC, got.MAC)
	require.Error(t, v6store.DeleteDNSRecord(ctx, intent.Key()))
	_, err6 := v6store.ApplyIPv6(ctx, v6request)
	require.Error(t, err6)
	out, err := svc.Handle(ctx, core.Input{Packet: packet, Iface: "eth0.10"})
	require.Error(t, err)
	require.Nil(t, out)
}
