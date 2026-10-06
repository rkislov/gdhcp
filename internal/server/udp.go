// Copyright 2026 Кислов Роман Сергеевич
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"context"
	"log/slog"
	"net"
	"syscall"

	"github.com/kislovrs/godhcp/internal/core"
	"github.com/kislovrs/godhcp/internal/dhcp"
	"golang.org/x/net/ipv4"
)

// Serve reads DHCPv4 datagrams until ctx is cancelled.
func Serve(ctx context.Context, addr string, svc *core.Service, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	udpAddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	if raw, err := conn.SyscallConn(); err == nil {
		_ = raw.Control(func(fd uintptr) {
			_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
		})
	}
	pc := ipv4.NewPacketConn(conn)
	control := pc.SetControlMessage(ipv4.FlagInterface, true) == nil
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	buf := make([]byte, 4096)
	for {
		n, cm, src, err := readPacket(pc, conn, buf, control)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Debug("dhcp read", "err", err)
			continue
		}
		pkt, err := dhcp.Parse(buf[:n])
		if err != nil {
			log.Debug("dhcp parse", "err", err, "from", src)
			continue
		}
		iface := ""
		if cm != nil && cm.IfIndex != 0 {
			if ifi, err := net.InterfaceByIndex(cm.IfIndex); err == nil {
				iface = ifi.Name
			}
		}
		out, err := svc.Handle(ctx, core.Input{Packet: pkt, Iface: iface})
		if err != nil {
			log.Error("dhcp handle", "err", err)
			continue
		}
		if out == nil || out.Packet == nil || out.Dest == nil {
			continue
		}
		raw, err := out.Packet.Marshal()
		if err != nil {
			log.Error("dhcp marshal", "err", err)
			continue
		}
		if control && cm != nil {
			_, _ = pc.WriteTo(raw, &ipv4.ControlMessage{IfIndex: cm.IfIndex}, out.Dest)
			continue
		}
		_, _ = conn.WriteTo(raw, out.Dest)
	}
}

func readPacket(pc *ipv4.PacketConn, conn *net.UDPConn, buf []byte, control bool) (int, *ipv4.ControlMessage, net.Addr, error) {
	if control {
		n, cm, src, err := pc.ReadFrom(buf)
		return n, cm, src, err
	}
	n, src, err := conn.ReadFrom(buf)
	return n, nil, src, err
}
