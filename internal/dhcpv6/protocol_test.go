// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0

package dhcpv6

import (
	"context"
	"encoding/binary"
	"log/slog"
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/kislovrs/godhcp/internal/config"
	"github.com/kislovrs/godhcp/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestLeaseLifecycle(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "leases.db")
	cfg := config.Config{DHCPv6: config.DHCPv6{Enabled: true, Pools: []config.IPv6Pool{{Interface: "lan", Prefix: "fd01::/64", Start: "fd01::10", End: "fd01::10"}}}}
	cfg.ApplyDefaults()
	open := func() (storage.Store, *engine) {
		db, err := storage.Open(ctx, "sqlite", file)
		require.NoError(t, err)
		e, err := newEngine(ctx, cfg.DHCPv6, db.(storage.IPv6Store), slog.Default())
		require.NoError(t, err)
		return db, e
	}
	db, e := open()
	cid := []byte{0, 3, 0, 1, 0, 1, 2, 3, 4, 5}
	peer := netip.MustParseAddr("fe80::42")
	call := func(mt byte, id, server []byte, hint string) []byte {
		packet := appendOption([]byte{mt, 1, 2, 3}, clientID, id)
		if server != nil {
			packet = appendOption(packet, serverID, server)
		}
		ia := make([]byte, 12)
		ia[3] = 1
		if hint != "" {
			ia = appendOption(ia, iaAddr, append(netip.MustParseAddr(hint).AsSlice(), make([]byte, 8)...))
		}
		packet = appendOption(packet, iaNA, ia)
		out, err := e.handle(ctx, packet, "lan", peer)
		require.NoError(t, err)
		return out
	}
	readIA := func(out []byte) (uint16, string) {
		require.GreaterOrEqual(t, len(out), 4)
		require.Equal(t, []byte{1, 2, 3}, out[1:4])
		opts, ok := parseOptions(out[4:])
		require.True(t, ok)
		ia, ok := unique(opts, iaNA)
		require.True(t, ok)
		require.GreaterOrEqual(t, len(ia), 12)
		opts, ok = parseOptions(ia[12:])
		require.True(t, ok)
		for _, o := range opts {
			if o.code == statusCode {
				return binary.BigEndian.Uint16(o.value), ""
			}
			if o.code == iaAddr {
				return 0, netip.AddrFrom16([16]byte(o.value[:16])).String()
			}
		}
		return 0, ""
	}
	offer := call(solicit, cid, nil, "")
	require.Equal(t, byte(advertise), offer[0])
	_, hint := readIA(offer)
	require.Equal(t, "fd01::10", hint)
	foreign := []byte{0, 3, 0, 2}
	require.Empty(t, call(request, cid, foreign, hint))
	offLink, _ := readIA(call(request, cid, e.duid, "fd02::1"))
	require.EqualValues(t, notOnLink, offLink)
	_, ip := readIA(call(request, cid, e.duid, hint))
	require.Equal(t, hint, ip)
	code, _ := readIA(call(solicit, foreign, nil, ""))
	require.EqualValues(t, noAddrsAvail, code)
	original := e.duid
	require.NoError(t, db.Close())
	db, e = open()
	defer db.Close()
	require.Equal(t, original, e.duid)
	_, ip = readIA(call(renew, cid, e.duid, hint))
	require.Equal(t, hint, ip)
	_, ip = readIA(call(rebind, cid, nil, hint))
	require.Equal(t, hint, ip)
	code, _ = readIA(call(release, foreign, e.duid, hint))
	require.EqualValues(t, noBinding, code)
	code, _ = readIA(call(release, cid, e.duid, "fd01::99"))
	require.EqualValues(t, noBinding, code)
	code, _ = readIA(call(release, cid, e.duid, hint))
	require.Zero(t, code)
	code, _ = readIA(call(renew, cid, e.duid, hint))
	require.EqualValues(t, noBinding, code)
	_, ip = readIA(call(request, foreign, e.duid, hint))
	require.Equal(t, hint, ip)
	code, _ = readIA(call(decline, foreign, e.duid, hint))
	require.Zero(t, code)
	code, _ = readIA(call(solicit, cid, nil, ""))
	require.EqualValues(t, noAddrsAvail, code)
}

func TestRelayScopeAndMalformedPackets(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DHCPv6: config.DHCPv6{Enabled: true, DNS: []string{"fd01::53"}, Pools: []config.IPv6Pool{{Interface: "uplink", Prefix: "fd01::/64", Start: "fd01::10", End: "fd01::20", Relays: []string{"fd02::1"}}}}}
	cfg.ApplyDefaults()
	db, err := storage.Open(ctx, "sqlite", filepath.Join(t.TempDir(), "db"))
	require.NoError(t, err)
	defer db.Close()
	e, err := newEngine(ctx, cfg.DHCPv6, db.(storage.IPv6Store), slog.Default())
	require.NoError(t, err)
	src := netip.MustParseAddr("fd02::1")
	inner := appendOption([]byte{information, 1, 2, 3}, oro, []byte{0, dnsServers})
	wrap := func(link string, inner []byte) []byte {
		b := append([]byte{relayForward, 0}, netip.MustParseAddr(link).AsSlice()...)
		b = append(b, netip.MustParseAddr("fe80::1").AsSlice()...)
		return appendOption(appendOption(b, interfaceID, []byte{0, 4, 2}), relayMessage, inner)
	}
	outer := wrap("fd01::1", inner)
	got, err := e.handle(ctx, outer, "uplink", src)
	require.NoError(t, err)
	require.NotEmpty(t, got)
	require.Equal(t, byte(relayReply), got[0])
	require.Equal(t, outer[1:34], got[1:34])
	opts, _ := parseOptions(got[34:])
	iid, _ := unique(opts, interfaceID)
	require.Equal(t, []byte{0, 4, 2}, iid)
	rep, _ := unique(opts, relayMessage)
	require.Equal(t, []byte{reply, 1, 2, 3}, rep[:4])
	opts, _ = parseOptions(rep[4:])
	dns, _ := unique(opts, dnsServers)
	require.Equal(t, netip.MustParseAddr("fd01::53").AsSlice(), dns)
	for _, tc := range []struct {
		b     []byte
		iface string
		src   netip.Addr
	}{
		{outer, "uplink", netip.MustParseAddr("fd02::2")}, {outer, "lan", src}, {wrap("fd03::1", inner), "uplink", src},
		{wrap("fd01::1", outer), "uplink", src}, {inner, "uplink", src}, {outer[:10], "uplink", src},
		{wrap("fd01::1", append(inner, 0xff)), "uplink", src}, {appendOption(outer, relayMessage, inner), "uplink", src},
	} {
		out, err := e.handle(ctx, tc.b, tc.iface, tc.src)
		require.NoError(t, err)
		require.Empty(t, out)
	}
}
