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

package core

import (
	"encoding/binary"
	"net"
	"net/netip"
	"time"

	"github.com/kislovrs/godhcp/internal/config"
	"github.com/kislovrs/godhcp/internal/dhcp"
)

func buildReply(cfg *config.Config, req *dhcp.Packet, sub *subnet, msgType byte, yi netip.Addr, lease time.Duration, text string) *dhcp.Packet {
	ch := make(net.HardwareAddr, 16)
	copy(ch, req.CHAddr)
	p := &dhcp.Packet{
		Op:     dhcp.BootReply,
		HType:  req.HType,
		HLen:   req.HLen,
		Hops:   req.Hops,
		XID:    req.XID,
		Secs:   req.Secs,
		Flags:  req.Flags,
		GIAddr: req.GIAddr,
		CHAddr: ch,
		SName:  cfg.Server.Hostname,
	}
	if req.HType == 0 {
		p.HType = dhcp.HTypeEthernet
	}
	if req.HLen == 0 {
		p.HLen = 6
	}
	if msgType == dhcp.MsgAck || msgType == dhcp.MsgInform {
		if req.CIAddr.IsValid() && !req.CIAddr.IsUnspecified() {
			p.CIAddr = req.CIAddr
		}
	}
	if msgType == dhcp.MsgOffer || msgType == dhcp.MsgAck {
		p.YIAddr = yi
	}
	serverID, _ := netip.ParseAddr(cfg.Server.ServerID)
	if sub != nil && sub.next.IsValid() {
		p.SIAddr = sub.next
	} else if serverID.IsValid() {
		p.SIAddr = serverID
	}
	if sub != nil && sub.raw.BootFile != "" {
		p.File = sub.raw.BootFile
	}

	p.Add(dhcp.OptMessageType, []byte{msgType})
	if serverID.Is4() {
		p.Add(dhcp.OptServerID, serverID.AsSlice())
	}
	if (msgType == dhcp.MsgOffer || msgType == dhcp.MsgAck) && lease > 0 {
		p.Add(dhcp.OptLeaseTime, dhcp.DurationOption(dhcp.OptLeaseTime, lease).Data)
		t1, t2 := renewalTimes(sub, lease)
		p.Add(dhcp.OptRenewalTime, dhcp.DurationOption(dhcp.OptRenewalTime, t1).Data)
		p.Add(dhcp.OptRebindingTime, dhcp.DurationOption(dhcp.OptRebindingTime, t2).Data)
	}
	if sub != nil && msgType != dhcp.MsgNak {
		if sub.prefix.IsValid() {
			p.Add(dhcp.OptSubnetMask, maskOf(sub.prefix).AsSlice())
		}
		if sub.gateway.Is4() {
			p.Add(dhcp.OptRouter, sub.gateway.AsSlice())
		}
		if len(sub.dns) > 0 {
			p.Options = append(p.Options, dhcp.IPOption(dhcp.OptDNS, sub.dns...))
		}
		if sub.raw.Domain != "" {
			p.Add(dhcp.OptDomainName, []byte(sub.raw.Domain))
		}
		if sub.raw.BootFile != "" {
			p.Add(dhcp.OptBootfileName, []byte(sub.raw.BootFile))
		}
		if sub.next.Is4() {
			p.Add(dhcp.OptTFTPServer, []byte(sub.next.String()))
		}
		skip := map[int]bool{1: true, 3: true, 6: true, 15: true, 51: true, 53: true, 54: true, 58: true, 59: true, 67: true}
		if sub.next.Is4() {
			skip[66] = true
		}
		for code, value := range sub.options {
			if skip[code] {
				continue
			}
			opt, err := dhcp.ParseConfiguredOption(code, value)
			if err != nil {
				continue
			}
			p.Options = append(p.Options, opt)
		}
	}
	if host := req.Hostname(); host != "" && msgType != dhcp.MsgNak {
		p.Add(dhcp.OptHostname, []byte(host))
	}
	if id, ok := req.Get(dhcp.OptClientID); ok {
		p.Add(dhcp.OptClientID, id)
	}
	if text != "" {
		p.Add(dhcp.OptMessage, []byte(text))
	}
	if cfg.EchoOption82() {
		for _, blob := range req.RelayAgentBlobs() {
			p.Add(dhcp.OptRelayAgent, blob)
		}
	}
	return p
}

func renewalTimes(sub *subnet, lease time.Duration) (time.Duration, time.Duration) {
	t1 := lease / 2
	t2 := lease * 7 / 8
	if sub != nil && sub.raw.T1 > 0 {
		t1 = sub.raw.T1
	}
	if sub != nil && sub.raw.T2 > 0 {
		t2 = sub.raw.T2
	}
	return t1, t2
}

func maskOf(p netip.Prefix) netip.Addr {
	bits := p.Bits()
	var m uint32
	if bits >= 32 {
		m = ^uint32(0)
	} else if bits > 0 {
		m = ^uint32(0) << uint(32-bits)
	}
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], m)
	return netip.AddrFrom4(b)
}

func destination(req *dhcp.Packet, yi netip.Addr) *net.UDPAddr {
	if req.Relayed() {
		return &net.UDPAddr{IP: append(net.IP(nil), req.GIAddr.AsSlice()...), Port: 67}
	}
	if req.CIAddr.IsValid() && !req.CIAddr.IsUnspecified() {
		return &net.UDPAddr{IP: append(net.IP(nil), req.CIAddr.AsSlice()...), Port: 68}
	}
	if req.Broadcast() || !yi.IsValid() || yi.IsUnspecified() {
		return &net.UDPAddr{IP: net.IPv4bcast, Port: 68}
	}
	return &net.UDPAddr{IP: append(net.IP(nil), yi.AsSlice()...), Port: 68}
}
