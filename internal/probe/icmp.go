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

package probe

import (
	"context"
	"net"
	"net/netip"
	"os"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// ICMP checks whether an address answers an echo request.
// A timeout is treated as "not in use". A permission error is returned to the caller.
type ICMP struct{}

// Reachable sends one echo. Timeout yields false, nil.
func (ICMP) Reachable(ctx context.Context, ip netip.Addr, timeout time.Duration) (bool, error) {
	if !ip.Is4() {
		return false, nil
	}
	conn, err := icmp.ListenPacket("udp4", "0.0.0.0")
	if err != nil {
		return false, err
	}
	defer conn.Close()
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Body: &icmp.Echo{ID: os.Getpid() & 0xffff, Seq: 1, Data: []byte("godhcp")},
	}
	raw, err := msg.Marshal(nil)
	if err != nil {
		return false, err
	}
	deadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	dst := &net.UDPAddr{IP: net.IP(ip.AsSlice())}
	if _, err := conn.WriteTo(raw, dst); err != nil {
		return false, err
	}
	buf := make([]byte, 1500)
	n, _, err := conn.ReadFrom(buf)
	if err != nil {
		return false, nil
	}
	parsed, err := icmp.ParseMessage(1, buf[:n])
	if err != nil {
		return false, nil
	}
	return parsed.Type == ipv4.ICMPTypeEchoReply, nil
}
