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
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/kislovrs/godhcp/internal/classify"
	"github.com/kislovrs/godhcp/internal/dhcp"
	"github.com/kislovrs/godhcp/internal/ha"
	"github.com/kislovrs/godhcp/internal/model"
	"github.com/kislovrs/godhcp/internal/pool"
	"github.com/kislovrs/godhcp/internal/relay"
)

// Input is one DHCP datagram plus the interface it arrived on.
type Input struct {
	Packet *dhcp.Packet
	Iface  string
	VLAN   *int
}

// Output is the reply to send. A nil Output means the server stays silent.
type Output struct {
	Packet *dhcp.Packet
	Dest   *net.UDPAddr
	VLAN   *int
	PCP    int
}

// Handle runs the DHCPv4 state machine for one packet.
func (s *Service) Handle(ctx context.Context, in Input) (*Output, error) {
	start := time.Now()
	defer func() { s.metrics.Duration.Observe(time.Since(start).Seconds()) }()
	if in.Packet == nil {
		return nil, nil
	}
	snap := s.current()
	if snap == nil {
		return nil, nil
	}
	return s.dispatch(ctx, snap, in)
}

func (s *Service) dispatch(ctx context.Context, snap *Snapshot, in Input) (*Output, error) {
	pkt := in.Packet
	mt := pkt.MessageType()
	if mt == 0 {
		s.log.Warn("dhcp packet without message type")
		return nil, nil
	}
	infos := parseRelay(pkt, s)
	circuit := firstCircuit(infos)
	remote := string(relay.FirstRemoteID(infos))
	link := relay.FirstLinkSelection(infos)
	mac := model.NormalizeMAC(pkt.MAC())

	if pkt.Relayed() {
		if !snap.Cfg.Relay.Enabled {
			s.log.Warn("dropped relayed packet", "reason", "relay disabled", "giaddr", pkt.GIAddr.String())
			return nil, nil
		}
		if int(pkt.Hops) > snap.Cfg.Relay.MaxRelayHops {
			s.metrics.Hops.Inc()
			s.log.Warn("dropped relayed packet", "reason", "hops exceeded", "hops", pkt.Hops, "giaddr", pkt.GIAddr.String())
			return nil, nil
		}
		if !relayTrusted(snap, pkt, remote) {
			s.metrics.Untrusted.Inc()
			s.log.Warn("dropped untrusted relay", "giaddr", pkt.GIAddr.String(), "remote_id", remote, "circuit_id", circuit)
			return nil, nil
		}
		if snap.Cfg.Relay.RequireOption82 && len(infos) == 0 {
			s.log.Warn("dropped relayed packet", "reason", "option 82 required", "giaddr", pkt.GIAddr.String())
			return nil, nil
		}
	}

	vlanID := in.VLAN
	if vlanID == nil && in.Iface != "" {
		if id, ok := vlanFromIface(snap, in.Iface); ok {
			vlanID = &id
		}
	}

	sub, source, unknown := selectSubnet(snap, pkt, circuit, link, mac, vlanID)
	if pkt.Relayed() {
		s.metrics.Relay.WithLabelValues(pkt.GIAddr.String(), truncate(remote, 64), vlanLabel(vlanID)).Inc()
		s.noteRelay(pkt.GIAddr.String(), circuit, remote, vlanID, unknown || sub == nil)
	}
	if sub == nil && unknown && snap.Cfg.Relay.UnknownVLANAction == "default-pool" {
		sub = snap.defaultSubnet()
		source = "default-pool"
	}
	if sub == nil && !pkt.Relayed() {
		if ip := clientAddr(pkt); ip.IsValid() {
			if by := snap.byIP(ip); by != nil {
				sub = by
				source = "client-ip"
			}
		}
	}
	if sub == nil && len(snap.Subnets) == 1 && !pkt.Relayed() && vlanID == nil {
		for _, only := range snap.Subnets {
			sub = only
		}
		source = "only"
	}
	labelsSubnet := "none"
	if sub != nil {
		labelsSubnet = sub.ID
		if vlanID == nil {
			vlanID = sub.VLAN
		}
	}
	s.metrics.Requests.WithLabelValues(dhcp.MessageName(mt), labelsSubnet, vlanLabel(vlanID)).Inc()

	if sub == nil {
		if snap.Cfg.Relay.UnknownVLANAction == "nak" && (mt == dhcp.MsgDiscover || mt == dhcp.MsgRequest) {
			s.log.Warn("nak for unknown vlan", "giaddr", addrString(pkt.GIAddr), "circuit_id", circuit, "vlan", vlanLabel(vlanID))
			return s.nak(snap, pkt, "unknown vlan"), nil
		}
		s.log.Warn("no subnet for request", "mac", mac, "giaddr", addrString(pkt.GIAddr), "circuit_id", circuit, "vlan", vlanLabel(vlanID), "source", source)
		return nil, nil
	}

	switch mt {
	case dhcp.MsgDiscover:
		return s.offer(ctx, snap, pkt, sub, mac, vlanID, circuit, remote, link, false)
	case dhcp.MsgRequest:
		return s.request(ctx, snap, pkt, sub, mac, vlanID, circuit, remote, link)
	case dhcp.MsgDecline:
		return s.decline(ctx, snap, pkt, sub, mac, vlanID, circuit, remote)
	case dhcp.MsgRelease:
		return s.release(ctx, pkt, sub, mac, vlanID, circuit, remote)
	case dhcp.MsgInform:
		out := s.ack(snap, pkt, sub, netip.Addr{}, 0, 0, 0, pkt.Hostname(), vlanID, false)
		s.log.Info("dhcp inform", "mac", mac, "subnet", sub.ID, "vlan", vlanLabel(vlanID))
		return out, nil
	default:
		return nil, nil
	}
}

func (s *Service) offer(ctx context.Context, snap *Snapshot, pkt *dhcp.Packet, sub *Subnet, mac string, vlanID *int, circuit, remote string, link netip.Addr, strict bool) (*Output, error) {
	if !s.answerNew(snap, mac, false) {
		return nil, nil
	}
	requested, _ := pkt.RequestedIP()
	var leased netip.Addr
	allocated := false
	for attempt := 0; attempt < 4; attempt++ {
		ip, err := s.pools.Allocate(sub.ID, mac, pkt.ClientID(), requested, strict)
		if err != nil {
			s.log.Warn("pool allocate failed", "subnet", sub.ID, "mac", mac, "err", err)
			return nil, nil
		}
		if s.needsPing(snap, sub, mac, ip) && !s.addressFree(ctx, snap, ip) {
			s.pools.Hold(ip.String())
			s.persist(ctx, snap, sub, model.Lease{
				IP: ip.String(), MAC: mac, ClientID: pkt.ClientID(), SubnetID: sub.ID,
				VLANID: vlanID, GIAddr: addrString(pkt.GIAddr), CircuitID: circuit, RemoteID: remote,
				LinkSelect: addrString(link), State: model.StateDeclined,
				ExpiresAt: s.now().Add(snap.Cfg.Server.DeclineHold),
			})
			requested = netip.Addr{}
			strict = false
			continue
		}
		leased = ip
		allocated = true
		break
	}
	if !allocated {
		return nil, nil
	}
	hostname := hostFor(sub, mac, pkt.ClientID(), pkt.Hostname())
	leaseFor := snap.Cfg.Server.OfferTTL
	if leaseFor <= 0 {
		leaseFor = time.Minute
	}
	s.persist(ctx, snap, sub, model.Lease{
		IP: leased.String(), MAC: mac, ClientID: pkt.ClientID(), Hostname: hostname, SubnetID: sub.ID,
		VLANID: vlanID, GIAddr: addrString(pkt.GIAddr), CircuitID: circuit, RemoteID: remote,
		LinkSelect: addrString(link), State: model.StateOffered, ExpiresAt: s.now().Add(leaseFor),
	})
	d, t1, t2 := sub.timers(pkt, snap.Cfg.Server.LeaseMax)
	s.log.Info("dhcp offer", "mac", mac, "ip", leased.String(), "subnet", sub.ID, "vlan", vlanLabel(vlanID), "giaddr", addrString(pkt.GIAddr), "circuit_id", circuit, "remote_id", remote)
	return s.ack(snap, pkt, sub, leased, d, t1, t2, hostname, vlanID, true), nil
}

func (s *Service) request(ctx context.Context, snap *Snapshot, pkt *dhcp.Packet, sub *Subnet, mac string, vlanID *int, circuit, remote string, link netip.Addr) (*Output, error) {
	if id, ok := snap.serverID(); ok {
		if got, has := pkt.ServerID(); has && got != id {
			return nil, nil
		}
	}
	want, hasWant := pkt.RequestedIP()
	if !hasWant && pkt.CIAddr.IsValid() && !pkt.CIAddr.IsUnspecified() {
		want = pkt.CIAddr
		hasWant = true
	}
	have := s.owns(ctx, sub.ID, mac, want)
	if !s.answerNew(snap, mac, have) {
		return nil, nil
	}
	if !hasWant {
		if snap.Cfg.Server.Authoritative {
			return s.nak(snap, pkt, "address required"), nil
		}
		return nil, nil
	}
	if by := snap.byIP(want); by != nil && by.ID != sub.ID && !pkt.Relayed() {
		sub = by
		if vlanID == nil {
			vlanID = sub.VLAN
		}
	}
	leased, err := s.pools.Allocate(sub.ID, mac, pkt.ClientID(), want, true)
	if err != nil {
		if snap.Cfg.Server.Authoritative {
			s.log.Warn("dhcp nak", "mac", mac, "ip", want.String(), "subnet", sub.ID, "err", err, "giaddr", addrString(pkt.GIAddr), "circuit_id", circuit)
			return s.nak(snap, pkt, "address unavailable"), nil
		}
		s.log.Info("ignored request", "mac", mac, "ip", want.String(), "err", err)
		return nil, nil
	}
	hostname := hostFor(sub, mac, pkt.ClientID(), pkt.Hostname())
	d, t1, t2 := sub.timers(pkt, snap.Cfg.Server.LeaseMax)
	rec := model.Lease{
		IP: leased.String(), MAC: mac, ClientID: pkt.ClientID(), Hostname: hostname, SubnetID: sub.ID,
		VLANID: vlanID, GIAddr: addrString(pkt.GIAddr), CircuitID: circuit, RemoteID: remote,
		LinkSelect: addrString(link), State: model.StateBound, ExpiresAt: s.now().Add(d),
	}
	s.persist(ctx, snap, sub, rec)
	if s.onBound != nil {
		go s.onBound(rec)
	}
	s.log.Info("dhcp ack", "mac", mac, "ip", leased.String(), "subnet", sub.ID, "vlan", vlanLabel(vlanID), "giaddr", addrString(pkt.GIAddr), "circuit_id", circuit, "remote_id", remote, "pool", sub.ID)
	return s.ack(snap, pkt, sub, leased, d, t1, t2, hostname, vlanID, false), nil
}

func (s *Service) decline(ctx context.Context, snap *Snapshot, pkt *dhcp.Packet, sub *Subnet, mac string, vlanID *int, circuit, remote string) (*Output, error) {
	ip, ok := pkt.RequestedIP()
	if !ok {
		ip = pkt.CIAddr
	}
	if ip.IsValid() {
		s.pools.Hold(ip.String())
		s.persist(ctx, snap, sub, model.Lease{
			IP: ip.String(), MAC: mac, ClientID: pkt.ClientID(), SubnetID: sub.ID, VLANID: vlanID,
			GIAddr: addrString(pkt.GIAddr), CircuitID: circuit, RemoteID: remote,
			State: model.StateDeclined, ExpiresAt: s.now().Add(snap.Cfg.Server.DeclineHold),
		})
		s.log.Warn("dhcp decline", "mac", mac, "ip", ip.String(), "subnet", sub.ID)
	}
	return nil, nil
}

func (s *Service) release(ctx context.Context, pkt *dhcp.Packet, sub *Subnet, mac string, vlanID *int, circuit, remote string) (*Output, error) {
	ip := pkt.CIAddr
	if !ip.IsValid() || ip.IsUnspecified() {
		if req, ok := pkt.RequestedIP(); ok {
			ip = req
		}
	}
	if !ip.IsValid() {
		return nil, nil
	}
	s.pools.Release(ip.String(), mac)
	_ = s.store.UpsertLease(ctx, model.Lease{
		IP: ip.String(), MAC: mac, ClientID: pkt.ClientID(), SubnetID: sub.ID, VLANID: vlanID,
		GIAddr: addrString(pkt.GIAddr), CircuitID: circuit, RemoteID: remote,
		State: model.StateReleased, ExpiresAt: s.now(),
	})
	s.log.Info("dhcp release", "mac", mac, "ip", ip.String(), "subnet", sub.ID)
	return nil, nil
}

func (s *Service) persist(ctx context.Context, snap *Snapshot, sub *Subnet, l model.Lease) {
	if l.CreatedAt.IsZero() {
		l.CreatedAt = s.now()
	}
	if err := s.store.UpsertLease(ctx, l); err != nil {
		s.log.Error("persist lease", "ip", l.IP, "err", err)
		s.pools.Release(l.IP, l.MAC)
	}
}

func (s *Service) needsPing(snap *Snapshot, sub *Subnet, mac string, ip netip.Addr) bool {
	if !snap.Cfg.Server.PingCheck || s.prober == nil {
		return false
	}
	if _, ok := sub.reservation(mac, ""); ok && sub.reservationIP(mac) == ip.String() {
		return false
	}
	return true
}

func (sub *Subnet) reservationIP(mac string) string {
	r, ok := sub.reservation(mac, "")
	if !ok {
		return ""
	}
	return r.IP
}

func (s *Service) addressFree(ctx context.Context, snap *Snapshot, ip netip.Addr) bool {
	up, err := s.prober.Reachable(ctx, ip, snap.Cfg.Server.PingTimeout)
	if err != nil {
		s.log.Warn("ping check skipped", "ip", ip.String(), "err", err)
		return true
	}
	return !up
}

func (s *Service) owns(ctx context.Context, subnet, mac string, ip netip.Addr) bool {
	if !ip.IsValid() {
		list, _, err := s.store.ListLeases(ctx, model.LeaseFilter{SubnetID: subnet, Limit: 500})
		if err != nil {
			return false
		}
		for _, l := range list {
			if l.MAC == mac && (l.State == model.StateBound || l.State == model.StateOffered) && l.ExpiresAt.After(s.now()) {
				return true
			}
		}
		return false
	}
	l, err := s.store.GetLease(ctx, ip.String())
	if err != nil {
		return false
	}
	return l.MAC == mac && (l.State == model.StateBound || l.State == model.StateOffered) && l.ExpiresAt.After(s.now())
}

func (s *Service) answerNew(snap *Snapshot, mac string, haveLease bool) bool {
	if !snap.Cfg.HA.Enabled || haveLease {
		return true
	}
	return ha.Answers(snap.Cfg.HA.Role, snap.Cfg.HA.Split, mac)
}

func (s *Service) nak(snap *Snapshot, pkt *dhcp.Packet, reason string) *Output {
	reply := baseReply(pkt)
	reply.Append(dhcp.ByteOption(dhcp.OptMessageType, dhcp.MsgNak))
	if id, ok := snap.serverID(); ok {
		reply.Append(dhcp.IPOption(dhcp.OptServerID, id))
		reply.SIAddr = id
	}
	reply.Append(dhcp.StringOption(dhcp.OptMessage, reason))
	if cid, ok := pkt.Get(dhcp.OptClientID); ok {
		reply.Append(dhcp.Option{Code: dhcp.OptClientID, Data: cid})
	}
	echo82(snap, pkt, reply)
	return &Output{Packet: reply, Dest: destination(pkt, netip.Addr{}), VLAN: nil}
}

func (s *Service) ack(snap *Snapshot, pkt *dhcp.Packet, sub *Subnet, yi netip.Addr, lease, t1, t2 time.Duration, hostname string, vlanID *int, offer bool) *Output {
	reply := baseReply(pkt)
	mt := byte(dhcp.MsgAck)
	if offer {
		mt = dhcp.MsgOffer
		reply.CIAddr = netip.Addr{}
	}
	if yi.IsValid() {
		reply.YIAddr = yi
	}
	reply.Append(dhcp.ByteOption(dhcp.OptMessageType, mt))
	if id, ok := snap.serverID(); ok {
		reply.Append(dhcp.IPOption(dhcp.OptServerID, id))
		if !sub.NextServer.IsValid() {
			reply.SIAddr = id
		}
	}
	if sub.NextServer.IsValid() {
		reply.SIAddr = sub.NextServer
	}
	if sub.BootFile != "" {
		reply.File = sub.BootFile
	}
	if lease > 0 && mt != dhcp.MsgInform {
		reply.Append(dhcp.DurationOption(dhcp.OptLeaseTime, lease))
		reply.Append(dhcp.DurationOption(dhcp.OptRenewalTime, t1))
		reply.Append(dhcp.DurationOption(dhcp.OptRebindingTime, t2))
	}
	if sub.Prefix.IsValid() {
		reply.Append(dhcp.IPOption(dhcp.OptSubnetMask, prefixMask(sub.Prefix)))
	}
	if sub.Gateway.IsValid() {
		reply.Append(dhcp.IPOption(dhcp.OptRouter, sub.Gateway))
	}
	if len(sub.DNS) > 0 {
		reply.Append(dhcp.IPOption(dhcp.OptDNS, sub.DNS...))
	}
	if sub.Domain != "" {
		reply.Append(dhcp.StringOption(dhcp.OptDomainName, sub.Domain))
	}
	if hostname != "" {
		reply.Append(dhcp.StringOption(dhcp.OptHostname, hostname))
	}
	for code, value := range sub.Options {
		if replyHas(reply, byte(code)) {
			continue
		}
		opt, err := dhcp.ParseConfiguredOption(code, value)
		if err != nil {
			s.log.Warn("skip option", "code", code, "err", err)
			continue
		}
		reply.Append(opt)
	}
	if sub.BootFile != "" && !replyHas(reply, dhcp.OptBootfileName) {
		reply.Append(dhcp.StringOption(dhcp.OptBootfileName, sub.BootFile))
	}
	if cid, ok := pkt.Get(dhcp.OptClientID); ok {
		reply.Append(dhcp.Option{Code: dhcp.OptClientID, Data: cid})
	}
	echo82(snap, pkt, reply)
	return &Output{Packet: reply, Dest: destination(pkt, yi), VLAN: vlanID, PCP: sub.PCP}
}

func (sub *Subnet) timers(pkt *dhcp.Packet, max time.Duration) (lease, t1, t2 time.Duration) {
	lease = sub.Lease
	if sec, ok := pkt.RequestedLease(); ok {
		want := time.Duration(sec) * time.Second
		if want > 0 && want < lease {
			lease = want
		}
	}
	if max > 0 && lease > max {
		lease = max
	}
	if lease < time.Minute {
		lease = time.Minute
	}
	t1 = lease / 2
	t2 = lease * 7 / 8
	if sub.T1 > 0 && sub.T1 < lease {
		t1 = sub.T1
	}
	if sub.T2 > 0 && sub.T2 < lease {
		t2 = sub.T2
	}
	return lease, t1, t2
}

func selectSubnet(snap *Snapshot, pkt *dhcp.Packet, circuit string, link netip.Addr, mac string, vlanID *int) (*Subnet, string, bool) {
	if class, ok := classify.Match(snap.Cfg.Classes, mac, pkt.VendorClass(), vlanID); ok {
		if sub := snap.Subnets[class.Subnet]; sub != nil {
			return sub, "class:" + class.Name, false
		}
	}
	if pkt.Relayed() {
		req := relay.Request{
			LinkSelection: link,
			CircuitID:     circuit,
			DefaultVLAN:   snap.Cfg.Relay.DefaultVLAN,
			DefaultPool:   snap.Cfg.Relay.DefaultPool,
		}
		if snap.Cfg.TrustGIAddr() {
			req.GIAddr = pkt.GIAddr
		}
		dec, ok := relay.Resolve(snap.Cfg.Relay.VLANSourcePriority, snap.Parsers, snap.Networks, snap.Routes, req)
		if ok {
			return snap.Subnets[dec.SubnetID], dec.Source, false
		}
		if dec.UnknownVLAN {
			return nil, dec.Source, true
		}
		return nil, "", false
	}
	if vlanID != nil {
		if sub := snap.byVLAN(*vlanID); sub != nil {
			return sub, "vlan", false
		}
		return nil, "vlan", true
	}
	return nil, "", false
}

func relayTrusted(snap *Snapshot, pkt *dhcp.Packet, remote string) bool {
	list := snap.Cfg.Relay.TrustedRelays
	if len(list) == 0 {
		return true
	}
	gi := pkt.GIAddr.String()
	for _, t := range list {
		if t.GIAddr == "" && t.RemoteID == "" {
			continue
		}
		giOK := t.GIAddr == "" || t.GIAddr == gi
		ridOK := t.RemoteID == "" || t.RemoteID == remote
		if giOK && ridOK {
			return true
		}
	}
	return false
}

func parseRelay(pkt *dhcp.Packet, s *Service) []*relay.Info {
	var infos []*relay.Info
	for _, blob := range pkt.RelayAgentBlobs() {
		info, err := relay.Parse(blob)
		if err != nil {
			s.metrics.Parser.WithLabelValues("option82").Inc()
			continue
		}
		infos = append(infos, info)
	}
	return infos
}

func firstCircuit(infos []*relay.Info) string {
	ids := relay.WalkCircuitIDs(infos)
	if len(ids) == 0 {
		return ""
	}
	return string(ids[0])
}

func vlanFromIface(snap *Snapshot, name string) (int, bool) {
	if id, ok := vlanName(name); ok {
		return id, true
	}
	for _, iface := range snap.Cfg.Server.Interfaces {
		if iface.Name != name {
			continue
		}
		if iface.Mode == "access" && iface.VLAN >= 1 && iface.VLAN <= 4094 {
			return iface.VLAN, true
		}
	}
	return 0, false
}

func vlanName(name string) (int, bool) {
	// delegated to the vlan package without an import cycle: local copy of the suffix rule
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			n := 0
			if i == len(name)-1 {
				return 0, false
			}
			for _, c := range name[i+1:] {
				if c < '0' || c > '9' {
					return 0, false
				}
				n = n*10 + int(c-'0')
			}
			if n >= 1 && n <= 4094 {
				return n, true
			}
		}
	}
	return 0, false
}

func clientAddr(pkt *dhcp.Packet) netip.Addr {
	if pkt.CIAddr.IsValid() && !pkt.CIAddr.IsUnspecified() {
		return pkt.CIAddr
	}
	if ip, ok := pkt.RequestedIP(); ok {
		return ip
	}
	return netip.Addr{}
}

func hostFor(sub *Subnet, mac, clientID, fromPkt string) string {
	if r, ok := sub.reservation(mac, clientID); ok && r.Hostname != "" {
		return r.Hostname
	}
	return fromPkt
}

func baseReply(req *dhcp.Packet) *dhcp.Packet {
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
		CIAddr: req.CIAddr,
		GIAddr: req.GIAddr,
		CHAddr: ch,
	}
	if p.HType == 0 {
		p.HType = dhcp.HTypeEthernet
	}
	if p.HLen == 0 {
		p.HLen = 6
	}
	return p
}

func destination(req *dhcp.Packet, yi netip.Addr) *net.UDPAddr {
	if req.Relayed() {
		return &net.UDPAddr{IP: ipv4(req.GIAddr), Port: 67}
	}
	if req.CIAddr.IsValid() && !req.CIAddr.IsUnspecified() {
		return &net.UDPAddr{IP: ipv4(req.CIAddr), Port: 68}
	}
	if req.Broadcast() || !yi.IsValid() {
		return &net.UDPAddr{IP: net.IPv4bcast, Port: 68}
	}
	return &net.UDPAddr{IP: ipv4(yi), Port: 68}
}

func ipv4(ip netip.Addr) net.IP {
	b := ip.As4()
	return net.IPv4(b[0], b[1], b[2], b[3]).To4()
}

func echo82(snap *Snapshot, req, reply *dhcp.Packet) {
	if !req.Relayed() || !snap.Cfg.EchoOption82() {
		return
	}
	for _, blob := range req.RelayAgentBlobs() {
		reply.Add(dhcp.OptRelayAgent, blob)
	}
}

func replyHas(p *dhcp.Packet, code byte) bool {
	_, ok := p.Get(code)
	return ok
}

func vlanLabel(id *int) string {
	if id == nil {
		return "0"
	}
	return itoa(*id)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Compile-time check that pool errors remain referenced by callers via Allocate.
var _ = pool.ErrExhausted
