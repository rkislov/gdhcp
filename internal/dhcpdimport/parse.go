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
	"net"
	"strconv"
	"strings"
	"time"
)

// Document is the subset of ISC dhcpd.conf understood by the importer.
type Document struct {
	Authoritative bool
	DefaultLease  time.Duration
	MaxLease      time.Duration
	Options       map[int]string
	Subnets       []SubnetDecl
	Hosts         []HostDecl
	Warnings      []string
	Files         []string
}

type SubnetDecl struct {
	Network      string
	Netmask      string
	RangeStart   string
	RangeEnd     string
	Gateway      string
	DNS          []string
	Domain       string
	NextServer   string
	BootFile     string
	Lease        time.Duration
	Options      map[int]string
	Reservations []HostDecl
	SharedNet    string
}

type HostDecl struct {
	Name     string
	MAC      string
	IP       string
	Hostname string
}

var namedOptions = map[string]int{
	"subnet-mask":                 1,
	"routers":                     3,
	"domain-name-servers":         6,
	"domain-name":                 15,
	"broadcast-address":           28,
	"ntp-servers":                 42,
	"vendor-encapsulated-options": 43,
	"dhcp-lease-time":             51,
	"dhcp-message-type":           53,
	"dhcp-server-identifier":      54,
	"tftp-server-name":            66,
	"bootfile-name":               67,
	"www-server":                  72,
}

// Parse parses an already-expanded dhcpd configuration text.
func Parse(src string) (*Document, error) {
	d := &Document{Options: map[int]string{}}
	p := &parser{src: src, doc: d}
	if err := p.parseBlock(nil); err != nil {
		return nil, err
	}
	return d, nil
}

type scope struct {
	subnet *SubnetDecl
	host   *HostDecl
	group  *groupScope
}

type groupScope struct {
	options    map[int]string
	gateway    string
	dns        []string
	domain     string
	nextServer string
	bootFile   string
	lease      time.Duration
}

type parser struct {
	src string
	i   int
	doc *Document
}

func (p *parser) parseBlock(sc *scope) error {
	for {
		p.skip()
		if p.i >= len(p.src) {
			return nil
		}
		if p.src[p.i] == '}' {
			p.i++
			return nil
		}
		if err := p.parseStatement(sc); err != nil {
			return err
		}
	}
}

func (p *parser) parseStatement(sc *scope) error {
	p.skip()
	if p.i >= len(p.src) {
		return nil
	}
	if p.src[p.i] == ';' {
		p.i++
		return nil
	}
	kw := p.readIdent()
	if kw == "" {
		p.warnf("skipping unexpected input near %q", snippet(p.src, p.i))
		p.skipToStatementEnd()
		return nil
	}
	switch strings.ToLower(kw) {
	case "authoritative":
		p.doc.Authoritative = true
		p.expectSemi()
	case "not":
		next := p.readIdent()
		if strings.EqualFold(next, "authoritative") {
			p.doc.Authoritative = false
			p.expectSemi()
		} else {
			p.warnf("unsupported statement: not %s", next)
			p.skipToStatementEnd()
		}
	case "default-lease-time":
		sec, err := p.readInt()
		if err != nil {
			return err
		}
		p.doc.DefaultLease = time.Duration(sec) * time.Second
		if sc != nil && sc.subnet != nil {
			sc.subnet.Lease = p.doc.DefaultLease
		}
		if sc != nil && sc.group != nil {
			sc.group.lease = p.doc.DefaultLease
		}
		p.expectSemi()
	case "max-lease-time":
		sec, err := p.readInt()
		if err != nil {
			return err
		}
		p.doc.MaxLease = time.Duration(sec) * time.Second
		p.expectSemi()
	case "option":
		if err := p.parseOption(sc); err != nil {
			return err
		}
	case "subnet":
		return p.parseSubnet(sc)
	case "shared-network":
		return p.parseSharedNetwork()
	case "host":
		return p.parseHost(sc)
	case "group":
		return p.parseGroup(sc)
	case "range":
		return p.parseRange(sc)
	case "hardware":
		return p.parseHardware(sc)
	case "fixed-address":
		return p.parseFixedAddress(sc)
	case "filename":
		val, err := p.readValueUntilSemi()
		if err != nil {
			return err
		}
		val = unquote(val)
		if sc != nil && sc.subnet != nil {
			sc.subnet.BootFile = val
		} else if sc != nil && sc.group != nil {
			sc.group.bootFile = val
		}
	case "next-server":
		val, err := p.readValueUntilSemi()
		if err != nil {
			return err
		}
		val = strings.TrimSpace(unquote(val))
		if sc != nil && sc.subnet != nil {
			sc.subnet.NextServer = val
		} else if sc != nil && sc.group != nil {
			sc.group.nextServer = val
		}
	case "include":
		// Should have been expanded; keep a clear error if one remains.
		path, _ := p.readValueUntilSemi()
		return fmt.Errorf("unexpanded include %s — expand files before Parse", path)
	default:
		p.warnf("unsupported statement %q skipped", kw)
		p.skipToStatementEnd()
	}
	return nil
}

func (p *parser) parseOption(sc *scope) error {
	p.skip()
	nameOrCode := p.readIdent()
	if nameOrCode == "" {
		return fmt.Errorf("option name expected near %q", snippet(p.src, p.i))
	}
	code := 0
	if strings.EqualFold(nameOrCode, "code") {
		n, err := p.readInt()
		if err != nil {
			return err
		}
		code = n
	} else if n, err := strconv.Atoi(nameOrCode); err == nil {
		code = n
	} else {
		c, ok := namedOptions[strings.ToLower(nameOrCode)]
		if !ok {
			p.warnf("unknown option %q skipped", nameOrCode)
			p.skipToStatementEnd()
			return nil
		}
		code = c
	}
	val, err := p.readValueUntilSemi()
	if err != nil {
		return err
	}
	val = strings.TrimSpace(unquote(val))
	val = strings.TrimSuffix(val, ";")
	val = strings.TrimSpace(val)
	p.applyOption(sc, code, val)
	return nil
}

func (p *parser) applyOption(sc *scope, code int, val string) {
	switch code {
	case 3:
		gw := firstCSV(val)
		if sc != nil && sc.subnet != nil {
			sc.subnet.Gateway = gw
		} else if sc != nil && sc.group != nil {
			sc.group.gateway = gw
		}
	case 6:
		dns := splitCSV(val)
		if sc != nil && sc.subnet != nil {
			sc.subnet.DNS = dns
		} else if sc != nil && sc.group != nil {
			sc.group.dns = dns
		}
	case 15:
		if sc != nil && sc.subnet != nil {
			sc.subnet.Domain = val
		} else if sc != nil && sc.group != nil {
			sc.group.domain = val
		}
	}
	if sc != nil && sc.subnet != nil {
		if sc.subnet.Options == nil {
			sc.subnet.Options = map[int]string{}
		}
		sc.subnet.Options[code] = val
		return
	}
	if sc != nil && sc.group != nil {
		if sc.group.options == nil {
			sc.group.options = map[int]string{}
		}
		sc.group.options[code] = val
		return
	}
	p.doc.Options[code] = val
}

func (p *parser) parseSubnet(parent *scope) error {
	netIP := p.readToken()
	if !strings.EqualFold(p.readIdent(), "netmask") {
		return fmt.Errorf("subnet %s: expected netmask", netIP)
	}
	mask := p.readToken()
	p.skip()
	if p.i >= len(p.src) || p.src[p.i] != '{' {
		return fmt.Errorf("subnet %s: expected '{'", netIP)
	}
	p.i++
	sub := SubnetDecl{
		Network: netIP,
		Netmask: mask,
		Options: map[int]string{},
	}
	if parent != nil && parent.group != nil {
		g := parent.group
		sub.Gateway = g.gateway
		sub.DNS = append([]string{}, g.dns...)
		sub.Domain = g.domain
		sub.NextServer = g.nextServer
		sub.BootFile = g.bootFile
		sub.Lease = g.lease
		for k, v := range g.options {
			sub.Options[k] = v
		}
	}
	if err := p.parseBlock(&scope{subnet: &sub}); err != nil {
		return err
	}
	p.doc.Subnets = append(p.doc.Subnets, sub)
	return nil
}

func (p *parser) parseSharedNetwork() error {
	name := unquote(p.readToken())
	p.skip()
	if p.i >= len(p.src) || p.src[p.i] != '{' {
		return fmt.Errorf("shared-network: expected '{'")
	}
	p.i++
	before := len(p.doc.Subnets)
	g := &groupScope{options: map[int]string{}}
	if err := p.parseBlock(&scope{group: g}); err != nil {
		return err
	}
	for i := before; i < len(p.doc.Subnets); i++ {
		p.doc.Subnets[i].SharedNet = name
		mergeGroupIntoSubnet(&p.doc.Subnets[i], g)
	}
	return nil
}

func (p *parser) parseHost(parent *scope) error {
	name := p.readToken()
	p.skip()
	if p.i >= len(p.src) || p.src[p.i] != '{' {
		return fmt.Errorf("host %s: expected '{'", name)
	}
	p.i++
	h := HostDecl{Name: name, Hostname: name}
	if err := p.parseBlock(&scope{host: &h}); err != nil {
		return err
	}
	if parent != nil && parent.subnet != nil {
		parent.subnet.Reservations = append(parent.subnet.Reservations, h)
	} else {
		p.doc.Hosts = append(p.doc.Hosts, h)
	}
	return nil
}

func (p *parser) parseGroup(parent *scope) error {
	p.skip()
	if p.i >= len(p.src) || p.src[p.i] != '{' {
		return fmt.Errorf("group: expected '{'")
	}
	p.i++
	g := &groupScope{options: map[int]string{}}
	if parent != nil && parent.group != nil {
		pg := parent.group
		g.gateway = pg.gateway
		g.dns = append([]string{}, pg.dns...)
		g.domain = pg.domain
		g.nextServer = pg.nextServer
		g.bootFile = pg.bootFile
		g.lease = pg.lease
		for k, v := range pg.options {
			g.options[k] = v
		}
	}
	before := len(p.doc.Subnets)
	if err := p.parseBlock(&scope{group: g}); err != nil {
		return err
	}
	for i := before; i < len(p.doc.Subnets); i++ {
		mergeGroupIntoSubnet(&p.doc.Subnets[i], g)
	}
	return nil
}

func mergeGroupIntoSubnet(sub *SubnetDecl, g *groupScope) {
	if g == nil {
		return
	}
	if sub.Gateway == "" {
		sub.Gateway = g.gateway
	}
	if len(sub.DNS) == 0 {
		sub.DNS = append([]string{}, g.dns...)
	}
	if sub.Domain == "" {
		sub.Domain = g.domain
	}
	if sub.NextServer == "" {
		sub.NextServer = g.nextServer
	}
	if sub.BootFile == "" {
		sub.BootFile = g.bootFile
	}
	if sub.Lease == 0 {
		sub.Lease = g.lease
	}
	for k, v := range g.options {
		if _, ok := sub.Options[k]; !ok {
			if sub.Options == nil {
				sub.Options = map[int]string{}
			}
			sub.Options[k] = v
		}
	}
}

func (p *parser) parseRange(sc *scope) error {
	start := p.readToken()
	end := p.readToken()
	if end == "" || end == ";" {
		end = start
	}
	p.expectSemi()
	if sc == nil || sc.subnet == nil {
		p.warnf("range outside subnet ignored: %s %s", start, end)
		return nil
	}
	if sc.subnet.RangeStart == "" {
		sc.subnet.RangeStart = start
		sc.subnet.RangeEnd = end
	} else {
		p.warnf("subnet %s: only first range is imported (%s-%s kept)", sc.subnet.Network, sc.subnet.RangeStart, sc.subnet.RangeEnd)
	}
	return nil
}

func (p *parser) parseHardware(sc *scope) error {
	kind := p.readIdent()
	val, err := p.readValueUntilSemi()
	if err != nil {
		return err
	}
	val = strings.TrimSpace(unquote(val))
	if !strings.EqualFold(kind, "ethernet") {
		p.warnf("hardware %s ignored", kind)
		return nil
	}
	if sc != nil && sc.host != nil {
		sc.host.MAC = normalizeMAC(val)
	}
	return nil
}

func (p *parser) parseFixedAddress(sc *scope) error {
	val, err := p.readValueUntilSemi()
	if err != nil {
		return err
	}
	ip := firstCSV(unquote(val))
	if sc != nil && sc.host != nil {
		sc.host.IP = ip
	}
	return nil
}

func (p *parser) skip() {
	for p.i < len(p.src) {
		if isSpace(p.src[p.i]) {
			p.i++
			continue
		}
		if p.src[p.i] == '#' {
			for p.i < len(p.src) && p.src[p.i] != '\n' {
				p.i++
			}
			continue
		}
		if strings.HasPrefix(p.src[p.i:], "//") {
			for p.i < len(p.src) && p.src[p.i] != '\n' {
				p.i++
			}
			continue
		}
		break
	}
}

func (p *parser) readIdent() string {
	p.skip()
	start := p.i
	for p.i < len(p.src) && isIdent(p.src[p.i]) {
		p.i++
	}
	return p.src[start:p.i]
}

func (p *parser) readToken() string {
	p.skip()
	if p.i >= len(p.src) {
		return ""
	}
	if p.src[p.i] == '"' || p.src[p.i] == '\'' {
		q := p.src[p.i]
		p.i++
		start := p.i
		for p.i < len(p.src) && p.src[p.i] != q {
			p.i++
		}
		val := p.src[start:p.i]
		if p.i < len(p.src) {
			p.i++
		}
		return val
	}
	start := p.i
	for p.i < len(p.src) {
		b := p.src[p.i]
		if isSpace(b) || b == '{' || b == '}' || b == ';' {
			break
		}
		p.i++
	}
	return p.src[start:p.i]
}

func (p *parser) readInt() (int, error) {
	tok := p.readToken()
	n, err := strconv.Atoi(tok)
	if err != nil {
		return 0, fmt.Errorf("integer expected, got %q", tok)
	}
	return n, nil
}

func (p *parser) readValueUntilSemi() (string, error) {
	p.skip()
	start := p.i
	for p.i < len(p.src) {
		if p.src[p.i] == '"' || p.src[p.i] == '\'' {
			q := p.src[p.i]
			p.i++
			for p.i < len(p.src) && p.src[p.i] != q {
				if p.src[p.i] == '\\' && p.i+1 < len(p.src) {
					p.i += 2
					continue
				}
				p.i++
			}
			if p.i < len(p.src) {
				p.i++
			}
			continue
		}
		if p.src[p.i] == ';' {
			val := strings.TrimSpace(p.src[start:p.i])
			p.i++
			return val, nil
		}
		if p.src[p.i] == '{' || p.src[p.i] == '}' {
			break
		}
		p.i++
	}
	return strings.TrimSpace(p.src[start:p.i]), nil
}

func (p *parser) expectSemi() {
	p.skip()
	if p.i < len(p.src) && p.src[p.i] == ';' {
		p.i++
	}
}

func (p *parser) skipToStatementEnd() {
	depth := 0
	for p.i < len(p.src) {
		switch p.src[p.i] {
		case '"', '\'':
			q := p.src[p.i]
			p.i++
			for p.i < len(p.src) && p.src[p.i] != q {
				p.i++
			}
			if p.i < len(p.src) {
				p.i++
			}
		case '{':
			depth++
			p.i++
		case '}':
			if depth == 0 {
				return
			}
			depth--
			p.i++
		case ';':
			p.i++
			if depth == 0 {
				return
			}
		default:
			p.i++
		}
	}
}

func (p *parser) warnf(format string, args ...any) {
	p.doc.Warnings = append(p.doc.Warnings, fmt.Sprintf(format, args...))
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(unquote(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstCSV(s string) string {
	parts := splitCSV(s)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func normalizeMAC(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, "-", ":")
	return s
}

// CIDR builds network/prefix from ISC subnet + netmask.
func CIDR(network, netmask string) (string, error) {
	ip := net.ParseIP(network)
	maskIP := net.ParseIP(netmask)
	if ip == nil || maskIP == nil {
		return "", fmt.Errorf("invalid subnet %s netmask %s", network, netmask)
	}
	ip4 := ip.To4()
	m4 := maskIP.To4()
	if ip4 == nil || m4 == nil {
		return "", fmt.Errorf("only IPv4 subnets are imported")
	}
	ones, _ := net.IPv4Mask(m4[0], m4[1], m4[2], m4[3]).Size()
	n := net.IPNet{IP: ip4.Mask(net.IPv4Mask(m4[0], m4[1], m4[2], m4[3])), Mask: net.IPv4Mask(m4[0], m4[1], m4[2], m4[3])}
	_ = ones
	return fmt.Sprintf("%s/%d", n.IP.String(), ones), nil
}
