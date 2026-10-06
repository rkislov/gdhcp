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

package dhcpdimport

import (
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/kislovrs/godhcp/internal/config"
)

// ImportFile expands includes, parses ISC dhcpd.conf and converts it to GoDHCP YAML config.
func ImportFile(path string) (*config.Config, *Document, error) {
	text, files, err := Expand(path)
	if err != nil {
		return nil, nil, err
	}
	doc, err := Parse(text)
	if err != nil {
		return nil, nil, err
	}
	doc.Files = files
	cfg, err := Convert(doc)
	if err != nil {
		return nil, doc, err
	}
	return cfg, doc, nil
}

// Convert maps a parsed dhcpd document onto a GoDHCP config with sensible defaults.
func Convert(doc *Document) (*config.Config, error) {
	cfg := &config.Config{
		Server: config.Server{
			Authoritative: doc.Authoritative,
			Listen:        ":67",
			LeaseDefault:  doc.DefaultLease,
			LeaseMax:      doc.MaxLease,
		},
		Database: config.Database{
			Driver: "sqlite",
			DSN:    "/var/lib/godhcp/leases.db",
		},
		API: config.API{
			Listen: ":8080",
			Auth: config.Auth{
				JWTSecret: "${JWT_SECRET:-change-me-after-import-32bytes}",
				TokenTTL:  time.Hour,
			},
		},
		Web:     config.Web{Enabled: true, Path: "/ui"},
		Relay:   config.Relay{Enabled: true},
		Logging: config.Logging{Level: "info", Format: "json"},
		Metrics: config.Metrics{Enabled: true, Listen: ":9090"},
		Options: map[int]string{},
	}
	for k, v := range doc.Options {
		cfg.Options[k] = v
	}
	if cfg.Server.LeaseDefault == 0 {
		cfg.Server.LeaseDefault = 24 * time.Hour
	}
	if cfg.Server.LeaseMax == 0 {
		cfg.Server.LeaseMax = 72 * time.Hour
	}
	if cfg.Server.LeaseMax < cfg.Server.LeaseDefault {
		cfg.Server.LeaseMax = cfg.Server.LeaseDefault
	}

	usedIDs := map[string]int{}
	for _, s := range doc.Subnets {
		cidr, err := CIDR(s.Network, s.Netmask)
		if err != nil {
			doc.Warnings = append(doc.Warnings, err.Error())
			continue
		}
		id := subnetID(cidr, s.SharedNet, usedIDs)
		sub := config.Subnet{
			ID:         id,
			Network:    cidr,
			Gateway:    s.Gateway,
			DNS:        append([]string{}, s.DNS...),
			Domain:     s.Domain,
			NextServer: s.NextServer,
			BootFile:   s.BootFile,
			Lease:      s.Lease,
			Options:    map[int]string{},
		}
		if s.RangeStart != "" {
			sub.Range = []string{s.RangeStart, s.RangeEnd}
		}
		for k, v := range s.Options {
			// Keep routers/dns/domain in dedicated fields; still copy other options.
			if k == 3 || k == 6 || k == 15 {
				continue
			}
			sub.Options[k] = v
		}
		for _, h := range s.Reservations {
			if r, ok := reservationFromHost(h, doc); ok {
				sub.Reservations = append(sub.Reservations, r)
			}
		}
		cfg.Subnets = append(cfg.Subnets, sub)
		if cfg.Server.ServerID == "" && sub.Gateway != "" {
			cfg.Server.ServerID = sub.Gateway
		}
	}

	for _, h := range doc.Hosts {
		r, ok := reservationFromHost(h, doc)
		if !ok {
			continue
		}
		ip, err := netip.ParseAddr(r.IP)
		if err != nil || !ip.Is4() {
			doc.Warnings = append(doc.Warnings, fmt.Sprintf("host %s: invalid IP %s", h.Name, r.IP))
			continue
		}
		placed := false
		for i := range cfg.Subnets {
			prefix, err := netip.ParsePrefix(cfg.Subnets[i].Network)
			if err != nil {
				continue
			}
			if prefix.Contains(ip) {
				cfg.Subnets[i].Reservations = append(cfg.Subnets[i].Reservations, r)
				placed = true
				break
			}
		}
		if !placed {
			if len(cfg.Subnets) == 0 {
				doc.Warnings = append(doc.Warnings, fmt.Sprintf("host %s (%s) skipped: no subnet to attach", h.Name, r.IP))
				continue
			}
			r.SubnetID = cfg.Subnets[0].ID
			cfg.Subnets[0].Reservations = append(cfg.Subnets[0].Reservations, r)
			doc.Warnings = append(doc.Warnings, fmt.Sprintf("host %s (%s) did not match a subnet; attached to %s", h.Name, r.IP, cfg.Subnets[0].ID))
		}
	}

	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("imported config failed validation: %w", err)
	}
	return cfg, nil
}

func reservationFromHost(h HostDecl, doc *Document) (config.Reservation, bool) {
	if h.IP == "" {
		doc.Warnings = append(doc.Warnings, fmt.Sprintf("host %s has no fixed-address", h.Name))
		return config.Reservation{}, false
	}
	if h.MAC == "" {
		doc.Warnings = append(doc.Warnings, fmt.Sprintf("host %s has no hardware ethernet", h.Name))
		return config.Reservation{}, false
	}
	return config.Reservation{
		MAC:      h.MAC,
		IP:       h.IP,
		Hostname: pickHostname(h),
	}, true
}

func subnetID(cidr, shared string, used map[string]int) string {
	base := strings.ReplaceAll(strings.ReplaceAll(cidr, ".", "-"), "/", "-")
	base = "subnet-" + base
	if shared != "" {
		base = sanitizeID(shared) + "-" + base
	}
	n := used[base]
	used[base] = n + 1
	if n == 0 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, n+1)
}

func sanitizeID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "net"
	}
	return out
}

func pickHostname(h HostDecl) string {
	if h.Hostname != "" {
		return h.Hostname
	}
	return h.Name
}
