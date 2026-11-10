// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0

// Package dhcpv6 serves stateful IA_NA and stateless configuration over IPv6.
package dhcpv6

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/netip"
	"time"

	"github.com/kislovrs/godhcp/internal/config"
	"github.com/kislovrs/godhcp/internal/storage"
)

const (
	solicit       = 1
	advertise     = 2
	request       = 3
	confirm       = 4
	renew         = 5
	rebind        = 6
	reply         = 7
	release       = 8
	decline       = 9
	information   = 11
	relayForward  = 12
	relayReply    = 13
	clientID      = 1
	serverID      = 2
	iaNA          = 3
	iaAddr        = 5
	oro           = 6
	relayMessage  = 9
	statusCode    = 13
	interfaceID   = 18
	dnsServers    = 23
	iaPD          = 25
	solMaxRT      = 82
	infMaxRT      = 83
	success       = 0
	noAddrsAvail  = 2
	noBinding     = 3
	notOnLink     = 4
	noPrefixAvail = 6
)

type option struct {
	code  uint16
	value []byte
}

func parseOptions(b []byte) ([]option, bool) {
	var result []option
	for len(b) > 0 {
		if len(b) < 4 {
			return nil, false
		}
		n := int(binary.BigEndian.Uint16(b[2:]))
		if n > len(b)-4 {
			return nil, false
		}
		result = append(result, option{binary.BigEndian.Uint16(b), b[4 : 4+n]})
		b = b[4+n:]
	}
	return result, true
}
func appendOption(b []byte, code uint16, value []byte) []byte {
	b = binary.BigEndian.AppendUint16(b, code)
	b = binary.BigEndian.AppendUint16(b, uint16(len(value)))
	return append(b, value...)
}
func status(b []byte, code uint16) []byte {
	return appendOption(b, statusCode, binary.BigEndian.AppendUint16(nil, code))
}
func unique(opts []option, code uint16) ([]byte, bool) {
	var value []byte
	for _, o := range opts {
		if o.code == code {
			if value != nil {
				return nil, false
			}
			value = o.value
		}
	}
	return value, true
}

type association struct {
	id    uint32
	kind  uint16
	hints []netip.Addr
}

func associations(opts []option) ([]association, bool) {
	var out []association
	seen := map[[2]uint32]bool{}
	for _, o := range opts {
		if o.code != iaNA && o.code != iaPD {
			continue
		}
		if len(o.value) < 12 || len(out) >= 32 {
			return nil, false
		}
		a := association{id: binary.BigEndian.Uint32(o.value), kind: o.code}
		key := [2]uint32{uint32(o.code), a.id}
		if seen[key] {
			return nil, false
		}
		seen[key] = true
		nested, ok := parseOptions(o.value[12:])
		if !ok {
			return nil, false
		}
		for _, n := range nested {
			if n.code != iaAddr {
				continue
			}
			if len(n.value) < 24 {
				return nil, false
			}
			if _, ok = parseOptions(n.value[24:]); !ok {
				return nil, false
			}
			ip := netip.AddrFrom16([16]byte(n.value[:16]))
			if ip.Is4In6() {
				return nil, false
			}
			a.hints = append(a.hints, ip)
		}
		out = append(out, a)
	}
	return out, true
}

type pool struct {
	prefix     netip.Prefix
	start, end netip.Addr
	iface      string
	relays     map[netip.Addr]bool
}
type engine struct {
	store            storage.IPv6Store
	duid             []byte
	pools            []pool
	dns              []byte
	preferred, valid uint32
	log              *slog.Logger
}

func newEngine(ctx context.Context, c config.DHCPv6, store storage.IPv6Store, log *slog.Logger) (*engine, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	id, err := c.DUID()
	if err != nil {
		return nil, err
	}
	id, err = store.InitIPv6(ctx, id)
	if err != nil {
		return nil, err
	}
	e := &engine{store: store, duid: id, preferred: c.Preferred, valid: c.Valid, log: log}
	for _, s := range c.DNS {
		e.dns = append(e.dns, netip.MustParseAddr(s).AsSlice()...)
	}
	for _, v := range c.Pools {
		p := pool{
			prefix: netip.MustParsePrefix(v.Prefix), start: netip.MustParseAddr(v.Start),
			end: netip.MustParseAddr(v.End), iface: v.Interface, relays: map[netip.Addr]bool{},
		}
		for _, s := range v.Relays {
			p.relays[netip.MustParseAddr(s)] = true
		}
		e.pools = append(e.pools, p)
	}
	return e, nil
}

// Handle only one relay hop. The configured source, receiving interface and
// relay link-address must all identify the same pool before touching a lease.
func (e *engine) handle(ctx context.Context, in []byte, iface string, peer netip.Addr) ([]byte, error) {
	if len(in) < 4 {
		return nil, nil
	}
	if in[0] != relayForward {
		for _, p := range e.pools {
			if p.iface == iface && len(p.relays) == 0 {
				return e.client(ctx, in, p)
			}
		}
		return nil, nil
	}
	if len(in) < 34 || in[1] != 0 {
		return nil, nil
	}
	link := netip.AddrFrom16([16]byte(in[2:18]))
	opts, ok := parseOptions(in[34:])
	if !ok {
		return nil, nil
	}
	inner, ok := unique(opts, relayMessage)
	if !ok || len(inner) < 4 {
		return nil, nil
	}
	iid, ok := unique(opts, interfaceID)
	if !ok || len(iid) > 1024 {
		return nil, nil
	}
	for _, p := range e.pools {
		if p.iface != iface || !p.relays[peer.WithZone("")] || !p.prefix.Contains(link) {
			continue
		}
		answer, err := e.client(ctx, inner, p)
		if err != nil || answer == nil {
			return nil, err
		}
		out := append([]byte{relayReply}, in[1:34]...)
		if iid != nil {
			out = appendOption(out, interfaceID, iid)
		}
		return appendOption(out, relayMessage, answer), nil
	}
	return nil, nil
}
func (e *engine) client(ctx context.Context, in []byte, p pool) ([]byte, error) {
	mt := in[0]
	opts, ok := parseOptions(in[4:])
	if !ok {
		return nil, nil
	}
	client, ok := unique(opts, clientID)
	if !ok || (client != nil && (len(client) < 3 || len(client) > 128)) || (client == nil && mt != information) {
		return nil, nil
	}
	sid, ok := unique(opts, serverID)
	if !ok {
		return nil, nil
	}
	switch mt {
	case solicit, confirm, rebind:
		if sid != nil {
			return nil, nil
		}
	case request, renew, release, decline:
		if !bytes.Equal(e.duid, sid) {
			return nil, nil
		}
	case information:
		if sid != nil && !bytes.Equal(e.duid, sid) {
			return nil, nil
		}
	default:
		return nil, nil
	}
	ias, ok := associations(opts)
	if !ok || (mt == information && len(ias) > 0) {
		return nil, nil
	}
	requested, ok := unique(opts, oro)
	if !ok || len(requested)%2 != 0 {
		return nil, nil
	}
	out := append([]byte{reply}, in[1:4]...)
	if mt == solicit {
		out[0] = advertise
	}
	out = appendOption(out, serverID, e.duid)
	if client != nil {
		out = appendOption(out, clientID, client)
	}
	if mt == confirm {
		found := false
		for _, ia := range ias {
			for _, ip := range ia.hints {
				found = true
				if !p.prefix.Contains(ip) {
					return status(out, notOnLink), nil
				}
			}
		}
		if !found {
			return nil, nil
		}
		return status(out, success), nil
	}
	for _, ia := range ias {
		result, err := e.answerIA(ctx, mt, ia, p, hex.EncodeToString(client))
		if err != nil {
			return nil, err
		}
		out = appendOption(out, ia.kind, result)
	}
	if mt == release || mt == decline {
		return status(out, success), nil
	}
	sent := map[uint16]bool{}
	for len(requested) > 0 {
		code := binary.BigEndian.Uint16(requested)
		requested = requested[2:]
		if sent[code] {
			continue
		}
		switch {
		case code == dnsServers && len(e.dns) > 0:
			out = appendOption(out, code, e.dns)
		case code == solMaxRT && mt == solicit, code == infMaxRT && mt == information:
			out = appendOption(out, code, binary.BigEndian.AppendUint32(nil, 3600))
		}
		sent[code] = true
	}
	return out, nil
}

func (e *engine) answerIA(ctx context.Context, mt byte, ia association, p pool, client string) ([]byte, error) {
	out := make([]byte, 12)
	binary.BigEndian.PutUint32(out, ia.id)
	if ia.kind == iaPD {
		return status(out, noPrefixAvail), nil
	}
	if mt == request {
		for _, hint := range ia.hints {
			if !p.prefix.Contains(hint) {
				return status(out, notOnLink), nil
			}
		}
	}
	r := storage.IPv6Request{Client: client, IAID: ia.id, Prefix: p.prefix.String(), Start: p.start, End: p.end, Hints: ia.hints, Now: time.Now(), Lifetime: time.Duration(e.valid) * time.Second}
	switch mt {
	case solicit:
		r.Action = storage.Advertise6
		r.Lifetime = time.Minute
	case request:
		r.Action = storage.Bind6
	case renew, rebind:
		r.Action = storage.Renew6
	case release:
		r.Action = storage.Release6
	case decline:
		r.Action = storage.Decline6
		r.Lifetime = time.Hour
	}
	ip, err := e.store.ApplyIPv6(ctx, r)
	if errors.Is(err, storage.ErrNoBinding6) {
		return status(out, noBinding), nil
	}
	if errors.Is(err, storage.ErrNoAddress6) {
		return status(out, noAddrsAvail), nil
	}
	if err != nil {
		return nil, err
	}
	e.log.Info("dhcpv6 lease", "message", mt, "duid", client, "iaid", ia.id, "ip", ip, "prefix", p.prefix, "interface", p.iface)
	if mt == release || mt == decline {
		return out, nil
	}
	binary.BigEndian.PutUint32(out[4:], e.preferred/2)
	binary.BigEndian.PutUint32(out[8:], uint32(uint64(e.preferred)*4/5))
	address := binary.BigEndian.AppendUint32(ip.AsSlice(), e.preferred)
	address = binary.BigEndian.AppendUint32(address, e.valid)
	return appendOption(out, iaAddr, address), nil
}
