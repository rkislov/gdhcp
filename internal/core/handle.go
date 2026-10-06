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
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/kislovrs/godhcp/internal/classify"
	"github.com/kislovrs/godhcp/internal/config"
	"github.com/kislovrs/godhcp/internal/dhcp"
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

// Output is the reply, or a nil Packet when the datagram is dropped.
type Output struct {
	Packet *dhcp.Packet
	Dest   *net.UDPAddr
	VLAN   *int
	PCP    int
	Subnet string
}

// Handle runs the DHCPv4 state machine for one packet.
func (s *Service) Handle(ctx context.Context, in Input) (*Output, error) {
	start := time.Now()
	defer func() {
		if s.metrics != nil {
			s.metrics.Duration.Observe(time.Since(start).Seconds())
		}
	}()
	if in.Packet == nil {
		return nil, errors.New("core: nil packet")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.snap == nil {
		return nil, errors.New("core: not configured")
	}
	return s.handleLocked(ctx, in)
}

func (s *Service) handleLocked(ctx context.Context, in Input) (*Output, error) {
	pkt := in.Packet
	cfg := s.snap.cfg
	mt := pkt.MessageType()
	mac := pkt.MAC()

	var infos []*relay.Info
	for _, blob := range pkt.RelayAgentBlobs() {
		info, err := relay.Parse(blob)
		if err != nil {
			s.log.Warn("option 82", "err", err)
			continue
		}
		infos = append(infos, info)
	}
	circuit := ""
	remote := ""
	var link netip.Addr
	if len(infos) > 0 {
		ids := relay.WalkCircuitIDs(infos)
		circuit = firstCircuit(s.snap.parsers, ids)
		if circuit == "" && len(ids) > 0 {
			s.metrics.ParserErrors.WithLabelValues("circuit-id").Inc()
		}
		remote = stringLabel(relay.FirstRemoteID(infos))
		link = relay.FirstLinkSelection(infos)
	}

	vlan := in.VLAN
	if vlan == nil {
		if v, ok := vlanFromConfig(cfg, in.Iface); ok {
			vlan = &v
		}
	}

	if pkt.Relayed() {
		if !cfg.Relay.Enabled {
			s.log.Warn("relay disabled", "giaddr", pkt.GIAddr.String(), "mac", mac)
			return nil, nil
		}
		if int(pkt.Hops) > cfg.Relay.MaxRelayHops {
			s.metrics.HopsExceeded.Inc()
			s.log.Warn("relay hops exceeded", "hops", pkt.Hops, "giaddr", pkt.GIAddr.String(), "mac", mac)
			return nil, nil
		}
		if !relayAllowed(cfg, pkt.GIAddr.String(), remote, len(infos) > 0) {
			s.metrics.Untrusted.Inc()
			s.log.Warn("untrusted relay", "giaddr", pkt.GIAddr.String(), "remote_id", remote, "circuit_id", circuit, "mac", mac)
			return nil, nil
		}
	}

	sub, vlan, unknown := s.selectSubnet(pkt, vlan, circuit, link)
	vlanLabel := vlanString(vlan)
	if pkt.Relayed() {
		s.noteRelay(pkt.GIAddr.String(), remote, circuit, vlan)
		s.metrics.RelayRequests.WithLabelValues(pkt.GIAddr.String(), remote, vlanLabel).Inc()
	}
	if unknown {
		if pkt.Relayed() {
			s.metrics.RelayUnknown.WithLabelValues(pkt.GIAddr.String()).Inc()
		}
		return s.unknownVLAN(ctx, cfg, pkt, vlan)
	}
	if sub == nil {
		s.metrics.Requests.WithLabelValues(dhcp.MessageName(mt), "", vlanLabel).Inc()
		s.log.Info("no subnet", "type", dhcp.MessageName(mt), "mac", mac, "iface", in.Iface, "vlan", vlanLabel, "giaddr", addrString(pkt.GIAddr))
		return s.unknownVLAN(ctx, cfg, pkt, vlan)
	}

	if classSubnet, ok := classify.Match(cfg.Classes, pkt.VendorClass(), mac, vlan); ok {
		if alt := s.snap.subs[classSubnet]; alt != nil {
			sub = alt
		}
	}

	s.metrics.Requests.WithLabelValues(dhcp.MessageName(mt), sub.raw.ID, vlanLabel).Inc()
	s.log.Info("dhcp",
		"type", dhcp.MessageName(mt),
		"mac", mac,
		"giaddr", addrString(pkt.GIAddr),
		"circuit_id", circuit,
		"remote_id", remote,
		"vlan", vlanLabel,
		"pool", sub.raw.ID,
		"iface", in.Iface,
	)

	switch mt {
	case dhcp.MsgDiscover:
		return s.onDiscover(ctx, cfg, pkt, sub, vlan, circuit, remote, link)
	case dhcp.MsgRequest:
		return s.onRequest(ctx, cfg, pkt, sub, vlan, circuit, remote, link)
	case dhcp.MsgDecline:
		return s.onDecline(ctx, pkt, sub, vlan)
	case dhcp.MsgRelease:
		return s.onRelease(ctx, pkt, sub)
	case dhcp.MsgInform:
		return s.onInform(cfg, pkt, sub, vlan)
	default:
		s.log.Debug("ignore dhcp type", "type", mt)
		return nil, nil
	}
}

func (s *Service) selectSubnet(pkt *dhcp.Packet, vlan *int, circuit string, link netip.Addr) (*subnet, *int, bool) {
	cfg := s.snap.cfg
	if pkt.Relayed() {
		prio := append([]string(nil), cfg.Relay.VLANSourcePriority...)
		if !cfg.TrustGIAddr() {
			filtered := make([]string, 0, len(prio))
			for _, src := range prio {
				if src != relay.SourceGIAddr {
					filtered = append(filtered, src)
				}
			}
			prio = filtered
		}
		gi := pkt.GIAddr
		if !cfg.TrustGIAddr() {
			gi = netip.Addr{}
		}
		dec, ok := relay.Resolve(prio, s.snap.parsers, s.snap.networks(), s.snap.routes, relay.Request{
			LinkSelection: link,
			CircuitID:     circuit,
			GIAddr:        gi,
			DefaultVLAN:   cfg.Relay.DefaultVLAN,
			DefaultPool:   cfg.Relay.DefaultPool,
		})
		if dec.UnknownVLAN {
			id := dec.UnknownID
			return nil, &id, true
		}
		if ok {
			return s.snap.subs[dec.SubnetID], dec.VLAN, false
		}
		return nil, vlan, false
	}
	if vlan != nil {
		if sub := s.subnetByVLAN(*vlan); sub != nil {
			return sub, vlan, false
		}
		return nil, vlan, true
	}
	if ip, ok := pkt.RequestedIP(); ok {
		if id, found := s.pools.SubnetOf(ip); found {
			return s.snap.subs[id], s.snap.subs[id].vlan, false
		}
	}
	if pkt.CIAddr.IsValid() && !pkt.CIAddr.IsUnspecified() {
		if id, found := s.pools.SubnetOf(pkt.CIAddr); found {
			return s.snap.subs[id], s.snap.subs[id].vlan, false
		}
	}
	if len(s.snap.subs) == 1 {
		for _, sub := range s.snap.subs {
			return sub, sub.vlan, false
		}
	}
	return nil, nil, false
}

func (s *Service) subnetByVLAN(id int) *subnet {
	for _, sub := range s.snap.subs {
		if sub.vlan != nil && *sub.vlan == id {
			return sub
		}
	}
	return nil
}

func (s *Service) unknownVLAN(ctx context.Context, cfg *config.Config, pkt *dhcp.Packet, vlan *int) (*Output, error) {
	action := cfg.Relay.UnknownVLANAction
	s.log.Warn("unknown vlan", "action", action, "mac", pkt.MAC(), "giaddr", addrString(pkt.GIAddr), "vlan", vlanString(vlan))
	switch action {
	case "nak":
		if pkt.MessageType() == dhcp.MsgDiscover || pkt.MessageType() == dhcp.MsgRequest || pkt.MessageType() == dhcp.MsgInform {
			reply := buildReply(cfg, pkt, nil, dhcp.MsgNak, netip.Addr{}, 0, "unknown vlan")
			return &Output{Packet: reply, Dest: destination(pkt, netip.Addr{}), VLAN: vlan}, nil
		}
	case "default-pool":
		if cfg.Relay.DefaultPool != "" {
			if sub := s.snap.subs[cfg.Relay.DefaultPool]; sub != nil {
				switch pkt.MessageType() {
				case dhcp.MsgDiscover:
					return s.onDiscover(ctx, cfg, pkt, sub, sub.vlan, "", "", netip.Addr{})
				case dhcp.MsgRequest:
					return s.onRequest(ctx, cfg, pkt, sub, sub.vlan, "", "", netip.Addr{})
				}
			}
		}
	}
	return nil, nil
}

func (s *Service) onDiscover(ctx context.Context, cfg *config.Config, pkt *dhcp.Packet, sub *subnet, vlan *int, circuit, remote string, link netip.Addr) (*Output, error) {
	hint, _ := pkt.RequestedIP()
	ip, err := s.offerAddress(ctx, cfg, sub, pkt.MAC(), pkt.ClientID(), hint, false)
	if err != nil {
		s.log.Warn("discover", "err", err, "mac", pkt.MAC(), "pool", sub.raw.ID)
		return nil, nil
	}
	leaseFor := s.leaseTime(cfg, sub, pkt)
	if err := s.persist(ctx, pkt, sub, vlan, ip, model.StateOffered, s.now().Add(cfg.Server.OfferTTL), circuit, remote, link); err != nil {
		s.pools.Release(ip.String(), pkt.MAC())
		return nil, err
	}
	s.log.Info("offer", "mac", pkt.MAC(), "ip", ip.String(), "pool", sub.raw.ID, "vlan", vlanString(vlan), "giaddr", addrString(pkt.GIAddr), "circuit_id", circuit, "remote_id", remote)
	s.refreshMetrics()
	reply := buildReply(cfg, pkt, sub, dhcp.MsgOffer, ip, leaseFor, "")
	return &Output{Packet: reply, Dest: destination(pkt, ip), VLAN: vlan, PCP: sub.pcp, Subnet: sub.raw.ID}, nil
}

func (s *Service) onRequest(ctx context.Context, cfg *config.Config, pkt *dhcp.Packet, sub *subnet, vlan *int, circuit, remote string, link netip.Addr) (*Output, error) {
	if sid, ok := pkt.ServerID(); ok && sid.String() != cfg.Server.ServerID {
		return nil, nil
	}
	requested, hasReq := pkt.RequestedIP()
	if !hasReq && pkt.CIAddr.IsValid() && !pkt.CIAddr.IsUnspecified() {
		requested = pkt.CIAddr
		hasReq = true
	}
	ip, err := s.offerAddress(ctx, cfg, sub, pkt.MAC(), pkt.ClientID(), requested, hasReq)
	if err != nil {
		if errors.Is(err, pool.ErrConflict) || errors.Is(err, pool.ErrNotInPool) || errors.Is(err, pool.ErrExhausted) {
			if cfg.Server.Authoritative {
				s.log.Info("nak", "mac", pkt.MAC(), "requested", addrString(requested), "pool", sub.raw.ID, "err", err)
				reply := buildReply(cfg, pkt, sub, dhcp.MsgNak, netip.Addr{}, 0, "requested address is not available")
				return &Output{Packet: reply, Dest: destination(pkt, netip.Addr{}), VLAN: vlan, PCP: sub.pcp, Subnet: sub.raw.ID}, nil
			}
			return nil, nil
		}
		return nil, err
	}
	leaseFor := s.leaseTime(cfg, sub, pkt)
	if err := s.persist(ctx, pkt, sub, vlan, ip, model.StateBound, s.now().Add(leaseFor), circuit, remote, link); err != nil {
		return nil, err
	}
	s.log.Info("ack", "mac", pkt.MAC(), "ip", ip.String(), "pool", sub.raw.ID, "vlan", vlanString(vlan), "giaddr", addrString(pkt.GIAddr), "circuit_id", circuit, "remote_id", remote)
	s.refreshMetrics()
	reply := buildReply(cfg, pkt, sub, dhcp.MsgAck, ip, leaseFor, "")
	return &Output{Packet: reply, Dest: destination(pkt, ip), VLAN: vlan, PCP: sub.pcp, Subnet: sub.raw.ID}, nil
}

func (s *Service) onDecline(ctx context.Context, pkt *dhcp.Packet, sub *subnet, vlan *int) (*Output, error) {
	ip, ok := pkt.RequestedIP()
	if !ok {
		return nil, nil
	}
	s.pools.Bind(sub.raw.ID, ip.String(), "", "")
	until := s.now().Add(s.snap.cfg.Server.DeclineHold)
	_ = s.persist(ctx, pkt, sub, vlan, ip, model.StateDeclined, until, "", "", netip.Addr{})
	s.log.Info("decline", "mac", pkt.MAC(), "ip", ip.String(), "pool", sub.raw.ID)
	return nil, nil
}

func (s *Service) onRelease(ctx context.Context, pkt *dhcp.Packet, sub *subnet) (*Output, error) {
	ip := pkt.CIAddr
	if !ip.IsValid() || ip.IsUnspecified() {
		if req, ok := pkt.RequestedIP(); ok {
			ip = req
		}
	}
	if !ip.IsValid() {
		return nil, nil
	}
	existing, err := s.store.GetLease(ctx, ip.String())
	if err == nil && existing.MAC == pkt.MAC() {
		existing.State = model.StateReleased
		existing.ExpiresAt = s.now()
		_ = s.store.UpsertLease(ctx, existing)
		s.pools.Release(ip.String(), pkt.MAC())
		s.refreshMetrics()
	}
	s.log.Info("release", "mac", pkt.MAC(), "ip", ip.String(), "pool", sub.raw.ID)
	return nil, nil
}

func (s *Service) onInform(cfg *config.Config, pkt *dhcp.Packet, sub *subnet, vlan *int) (*Output, error) {
	reply := buildReply(cfg, pkt, sub, dhcp.MsgAck, netip.Addr{}, 0, "")
	return &Output{Packet: reply, Dest: destination(pkt, pkt.CIAddr), VLAN: vlan, PCP: sub.pcp, Subnet: sub.raw.ID}, nil
}

func (s *Service) offerAddress(ctx context.Context, cfg *config.Config, sub *subnet, mac, clientID string, hint netip.Addr, strict bool) (netip.Addr, error) {
	for attempt := 0; attempt < 4; attempt++ {
		ip, err := s.pools.Allocate(sub.raw.ID, mac, clientID, hint, strict)
		if err != nil {
			return netip.Addr{}, err
		}
		if !cfg.Server.PingCheck || s.isReservation(sub, mac, clientID, ip) {
			return ip, nil
		}
		inUse, err := s.probe.InUse(ctx, ip, cfg.Server.PingTimeout)
		if err != nil || !inUse {
			return ip, nil
		}
		s.log.Info("ping check", "ip", ip.String(), "in_use", true)
		s.pools.Hold(ip.String())
		_ = s.store.UpsertLease(ctx, model.Lease{
			IP: ip.String(), MAC: mac, SubnetID: sub.raw.ID, VLANID: sub.vlan,
			State: model.StateDeclined, ExpiresAt: s.now().Add(cfg.Server.DeclineHold), CreatedAt: s.now(),
		})
		if strict {
			return netip.Addr{}, pool.ErrConflict
		}
		hint = netip.Addr{}
	}
	return netip.Addr{}, pool.ErrExhausted
}

func (s *Service) isReservation(sub *subnet, mac, clientID string, ip netip.Addr) bool {
	for _, r := range sub.raw.Reservations {
		if r.IP == ip.String() && (normMAC(r.MAC) == mac || (clientID != "" && r.ClientID == clientID)) {
			return true
		}
	}
	return false
}

func (s *Service) persist(ctx context.Context, pkt *dhcp.Packet, sub *subnet, vlan *int, ip netip.Addr, state string, exp time.Time, circuit, remote string, link netip.Addr) error {
	l := model.Lease{
		IP: ip.String(), MAC: pkt.MAC(), ClientID: pkt.ClientID(), Hostname: pkt.Hostname(),
		SubnetID: sub.raw.ID, VLANID: vlan, State: state, ExpiresAt: exp, CreatedAt: s.now(),
		CircuitID: circuit, RemoteID: remote,
	}
	if pkt.Relayed() {
		l.GIAddr = pkt.GIAddr.String()
	}
	if link.IsValid() {
		l.LinkSelect = link.String()
	}
	if state == model.StateDeclined {
		l.MAC = pkt.MAC()
	}
	return s.store.UpsertLease(ctx, l)
}

func (s *Service) leaseTime(cfg *config.Config, sub *subnet, pkt *dhcp.Packet) time.Duration {
	d := sub.raw.Lease
	if d <= 0 {
		d = cfg.Server.LeaseDefault
	}
	if sec, ok := pkt.RequestedLease(); ok && sec > 0 {
		want := time.Duration(sec) * time.Second
		if want < d {
			d = want
		}
	}
	if cfg.Server.LeaseMax > 0 && d > cfg.Server.LeaseMax {
		d = cfg.Server.LeaseMax
	}
	if d < time.Minute {
		d = time.Minute
	}
	return d
}

func (s *Service) noteRelay(giaddr, remote, circuit string, vlan *int) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	st := s.relayStats[giaddr]
	if st == nil {
		st = &model.RelayStats{GIAddr: giaddr}
		s.relayStats[giaddr] = st
	}
	st.Requests++
	st.LastSeen = s.now()
	st.LastCircuit = circuit
	st.LastRemoteID = remote
	st.LastVLAN = vlan
}

func relayAllowed(cfg *config.Config, giaddr, remote string, has82 bool) bool {
	if cfg.Relay.RequireOption82 && !has82 {
		return false
	}
	if len(cfg.Relay.TrustedRelays) == 0 {
		return true
	}
	for _, t := range cfg.Relay.TrustedRelays {
		if t.GIAddr != "" && t.GIAddr != giaddr {
			continue
		}
		if t.RemoteID != "" && t.RemoteID != remote {
			continue
		}
		if t.GIAddr == "" && t.RemoteID == "" {
			continue
		}
		return true
	}
	return false
}

func firstCircuit(parsers []relay.Parser, ids [][]byte) string {
	for _, id := range ids {
		if _, _, _, ok := relay.MatchVLAN(parsers, string(id), ""); ok {
			return string(id)
		}
		if _, ok := relay.BinaryVLAN(id); ok {
			return string(id)
		}
	}
	return ""
}

func vlanFromConfig(cfg *config.Config, iface string) (int, bool) {
	if v, ok := vlanFromName(iface); ok {
		return v, true
	}
	for _, inf := range cfg.Server.Interfaces {
		if inf.Name != iface {
			continue
		}
		if inf.Mode == "access" && inf.VLAN > 0 {
			return inf.VLAN, true
		}
	}
	return 0, false
}

func vlanFromName(name string) (int, bool) {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			n, err := strconv.Atoi(name[i+1:])
			if err == nil && n >= 1 && n <= 4094 {
				return n, true
			}
			return 0, false
		}
	}
	return 0, false
}

func vlanString(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

func addrString(a netip.Addr) string {
	if !a.IsValid() || a.IsUnspecified() {
		return ""
	}
	return a.String()
}

func stringLabel(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return hex.EncodeToString(b)
		}
	}
	return string(b)
}
