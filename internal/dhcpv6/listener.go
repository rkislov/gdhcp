// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0

package dhcpv6

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"

	"github.com/kislovrs/godhcp/internal/config"
	"github.com/kislovrs/godhcp/internal/storage"
	"golang.org/x/net/ipv6"
)

type Listener struct {
	conn       *ipv6.PacketConn
	engine     *engine
	interfaces map[int]string
}

func Listen(ctx context.Context, c config.DHCPv6, store storage.Store, logger *slog.Logger) (*Listener, error) {
	db, ok := store.(storage.IPv6Store)
	if !ok {
		return nil, fmt.Errorf("storage does not support IPv6 leases")
	}
	e, err := newEngine(ctx, c, db, logger)
	if err != nil {
		return nil, err
	}
	addr, err := net.ResolveUDPAddr("udp6", c.Listen)
	if err != nil {
		return nil, err
	}
	udp, err := net.ListenUDP("udp6", addr)
	if err != nil {
		return nil, err
	}
	l := &Listener{ipv6.NewPacketConn(udp), e, map[int]string{}}
	if err = l.conn.SetControlMessage(ipv6.FlagInterface|ipv6.FlagDst, true); err != nil {
		l.Close()
		return nil, err
	}
	for _, p := range e.pools {
		iface, err := net.InterfaceByName(p.iface)
		if err != nil {
			l.Close()
			return nil, err
		}
		if _, exists := l.interfaces[iface.Index]; exists {
			continue
		}
		if err = l.conn.JoinGroup(iface, &net.UDPAddr{IP: net.ParseIP("ff02::1:2")}); err != nil {
			l.Close()
			return nil, err
		}
		l.interfaces[iface.Index] = iface.Name
	}
	return l, nil
}
func (l *Listener) Close() error { return l.conn.Close() }
func (l *Listener) Serve(ctx context.Context) error {
	buf := make([]byte, 65535)
	for {
		n, cm, src, err := l.conn.ReadFrom(buf)
		if err != nil {
			return err
		}
		if cm == nil || n < 4 || ctx.Err() != nil {
			continue
		}
		iface, ok := l.interfaces[cm.IfIndex]
		if !ok {
			continue
		}
		remote := src.(*net.UDPAddr)
		peer := remote.AddrPort().Addr()
		port := 546
		if buf[0] == relayForward {
			port = 547
		} else if !cm.Dst.Equal(net.ParseIP("ff02::1:2")) {
			continue
		}
		if !peer.Is6() || peer.Is4In6() || peer.IsUnspecified() || peer.IsMulticast() {
			continue
		}
		answer, err := l.engine.handle(ctx, buf[:n], iface, peer)
		if err != nil {
			l.engine.log.Error("dhcpv6 request failed", "err", err)
			continue
		}
		if answer == nil {
			continue
		}
		if peer.IsLinkLocalUnicast() {
			peer = peer.WithZone(iface)
		}
		dest := net.UDPAddrFromAddrPort(netip.AddrPortFrom(peer, uint16(port)))
		if _, err = l.conn.WriteTo(answer, &ipv6.ControlMessage{IfIndex: cm.IfIndex}, dest); err != nil {
			l.engine.log.Warn("dhcpv6 reply failed", "destination", dest, "err", err)
		}
	}
}
