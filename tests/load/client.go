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

//go:build loadtest

// Open-loop wire generator. Run only inside the isolated load-test container.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/kislovrs/godhcp/internal/dhcp"
)

type sample struct {
	sent       time.Time
	actualSent time.Time
	received   bool
	client     int
}

func main() {
	mode := flag.String("mode", "direct", "direct or relay")
	kind := flag.String("kind", "renew", "renew, discover or inform")
	rate := flag.Int("rate", 1000, "scheduled packets per second")
	seconds := flag.Int("seconds", 10, "sending duration")
	clients := flag.Int("clients", 100000, "client identities")
	drain := flag.Int("drain", 5, "reply drain seconds")
	flag.Parse()
	if *rate <= 0 || *seconds <= 0 || *clients <= 0 || *clients > 100000 || *rate**seconds > 2000000 || (*mode != "direct" && *mode != "relay") || (*kind != "renew" && *kind != "discover" && *kind != "inform") {
		fmt.Fprintln(os.Stderr, "invalid load parameters")
		os.Exit(2)
	}
	port := 68
	if *mode == "relay" {
		port = 67
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	conn.SetReadBuffer(16 << 20)
	conn.SetWriteBuffer(4 << 20)
	dest := &net.UDPAddr{IP: net.ParseIP("198.18.0.1"), Port: 67}
	total := *rate * *seconds
	samples := make([]sample, total+1)
	var mu sync.Mutex
	latencies := make([]float64, 0, total)
	rtts := make([]float64, 0, total)
	lags := make([]float64, 0, total)
	var slow []map[string]any
	var slowCount int
	var maxLatency float64
	var maxSample map[string]any
	var sampleSecond int64
	var samplesThisSecond int
	valid, invalid, duplicates, sendErrors := 0, 0, 0, 0
	done := make(chan struct{})
	agent := []byte{1, 8, 'l', 'o', 'a', 'd', ':', '1', '0', '0', 2, 9, 'l', 'o', 'a', 'd', 'r', 'e', 'l', 'a', 'y'}
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			now := time.Now()
			p, err := dhcp.Parse(buf[:n])
			mu.Lock()
			if err != nil || p.XID == 0 || int(p.XID) > total {
				invalid++
				mu.Unlock()
				continue
			}
			s := &samples[p.XID]
			if s.sent.IsZero() {
				invalid++
				mu.Unlock()
				continue
			}
			if s.received {
				duplicates++
				mu.Unlock()
				continue
			}
			expected := byte(dhcp.MsgAck)
			if *kind == "discover" {
				expected = dhcp.MsgOffer
			}
			if p.Op != dhcp.BootReply || p.MessageType() != expected || !bytes.Equal(p.CHAddr[:6], clientMAC(s.client)) || (*kind != "inform" && *kind != "discover" && p.YIAddr != clientIP(s.client)) {
				invalid++
				mu.Unlock()
				continue
			}
			if *mode == "relay" {
				v, ok := p.Get(dhcp.OptRelayAgent)
				if !ok || !bytes.Equal(v, agent) || p.GIAddr != netip.MustParseAddr("198.18.0.2") {
					invalid++
					mu.Unlock()
					continue
				}
			}
			s.received = true
			valid++
			latency := float64(now.Sub(s.sent)) / float64(time.Millisecond)
			rtt := float64(now.Sub(s.actualSent)) / float64(time.Millisecond)
			latencies = append(latencies, latency)
			rtts = append(rtts, rtt)
			if latency > maxLatency {
				maxLatency = latency
				maxSample = map[string]any{"xid": p.XID, "scheduled_unix_ns": s.sent.UnixNano(), "sent_unix_ns": s.actualSent.UnixNano(), "received_unix_ns": now.UnixNano(), "latency_ms": latency, "rtt_ms": rtt}
			}
			if latency >= 20 {
				slowCount++
			}
			if now.Unix() != sampleSecond {
				sampleSecond = now.Unix()
				samplesThisSecond = 0
			}
			if latency >= 20 && len(slow) < 1000 && samplesThisSecond < 20 {
				samplesThisSecond++
				slow = append(slow, map[string]any{"xid": p.XID, "scheduled_unix_ns": s.sent.UnixNano(), "sent_unix_ns": s.actualSent.UnixNano(), "received_unix_ns": now.UnixNano(), "latency_ms": latency, "rtt_ms": rtt})
			}
			mu.Unlock()
		}
	}()
	start := time.Now()
	for i := 1; i <= total; i++ {
		scheduled := start.Add(time.Duration(float64(i-1) * float64(time.Second) / float64(*rate)))
		if delay := time.Until(scheduled); delay > 0 {
			time.Sleep(delay)
		}
		id := (i - 1) % *clients
		mt := byte(dhcp.MsgRequest)
		if *kind == "discover" {
			mt = dhcp.MsgDiscover
		}
		if *kind == "inform" {
			mt = dhcp.MsgInform
		}
		p := dhcp.NewRequest(mt, clientMAC(id), uint32(i))
		if *kind == "renew" || *kind == "inform" {
			p.CIAddr = clientIP(id)
		} else {
			p.SetBroadcast(true)
		}
		if *mode == "relay" {
			p.GIAddr = netip.MustParseAddr("198.18.0.2")
			p.Hops = 1
			p.Append(dhcp.Option{Code: dhcp.OptRelayAgent, Data: agent})
		}
		raw, err := p.Marshal()
		if err != nil {
			panic(err)
		}
		mu.Lock()
		actualSent := time.Now()
		samples[i] = sample{sent: scheduled, actualSent: actualSent, client: id}
		lags = append(lags, float64(actualSent.Sub(scheduled))/float64(time.Millisecond))
		mu.Unlock()
		if _, err = conn.WriteToUDP(raw, dest); err != nil {
			sendErrors++
		}
	}
	sending := time.Since(start)
	deadline := time.Now().Add(time.Duration(*drain) * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := valid + sendErrors
		mu.Unlock()
		if count >= total {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	elapsed := time.Since(start)
	conn.Close()
	<-done
	sort.Float64s(latencies)
	sort.Float64s(rtts)
	sort.Float64s(lags)
	pct := func(q float64) float64 {
		if len(latencies) == 0 {
			return 0
		}
		return latencies[int(float64(len(latencies)-1)*q)]
	}
	report := map[string]any{"mode": *mode, "kind": *kind, "target_rps": *rate, "clients": *clients, "sent": total, "send_errors": sendErrors, "valid_replies": valid, "invalid_replies": invalid, "duplicate_replies": duplicates, "unanswered": total - sendErrors - valid, "sending_seconds": sending.Seconds(), "elapsed_seconds": elapsed.Seconds(), "actual_send_rps": float64(total) / sending.Seconds(), "reply_rps_including_drain": float64(valid) / elapsed.Seconds(), "latency_origin": "scheduled_send", "latency_ms": map[string]float64{"p50": pct(.5), "p95": pct(.95), "p99": pct(.99)}, "drain_seconds": *drain}
	percentile := func(values []float64) float64 {
		if len(values) == 0 {
			return 0
		}
		return values[int(float64(len(values)-1)*.99)]
	}
	report["actual_rtt_p99_ms"] = percentile(rtts)
	report["send_lag_p99_ms"] = percentile(lags)
	report["slow_samples"] = slow
	report["slow_replies_ge_20ms"] = slowCount
	report["max_latency_ms"] = maxLatency
	report["max_latency_sample"] = maxSample
	json.NewEncoder(os.Stdout).Encode(report)
}
func clientMAC(id int) net.HardwareAddr {
	return net.HardwareAddr{2, 0, 0, byte(id >> 16), byte(id >> 8), byte(id)}
}
func clientIP(id int) netip.Addr {
	n := uint32(0xc612000a) + uint32(id)
	return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
}
