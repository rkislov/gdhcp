// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-ldap/ldap/v3"
	"github.com/kislovrs/godhcp/internal/auth"
	"github.com/kislovrs/godhcp/internal/config"
	"github.com/kislovrs/godhcp/internal/model"
)

func (s *Server) resolvePrincipal(p auth.Principal) (auth.Principal, bool) {
	cfg := s.svc.Config().API.Auth
	p.Grants = nil
	p.Role = ""
	if p.Source == "ldap" {
		if !cfg.LDAP.Enabled {
			return p, false
		}
		// A configured local name always belongs to the local identity namespace.
		for _, u := range cfg.Users {
			if u.Username == p.Name {
				return p, false
			}
		}
		for _, g := range cfg.Groups {
			if g.LDAPDN == "" {
				continue
			}
			want, e := ldap.ParseDN(g.LDAPDN)
			if e != nil {
				continue
			}
			for _, dn := range p.DirectoryGroups {
				got, e := ldap.ParseDN(dn)
				if e == nil && want.EqualFold(got) {
					p.Grants = append(p.Grants, auth.Grant{Role: g.Role, VLANIDs: g.VLANIDs})
					break
				}
			}
		}
	} else {
		found := false
		for _, u := range cfg.Users {
			if u.Username != p.Name {
				continue
			}
			found = true
			if u.Role != "" {
				p.Grants = append(p.Grants, auth.Grant{Role: u.Role, VLANIDs: u.VLANIDs})
			}
			for _, name := range u.Groups {
				for _, g := range cfg.Groups {
					if g.Name == name {
						p.Grants = append(p.Grants, auth.Grant{Role: g.Role, VLANIDs: g.VLANIDs})
					}
				}
			}
			break
		}
		if !found {
			return p, false
		}
	}
	for _, g := range p.Grants {
		if auth.Rank(g.Role) > auth.Rank(p.Role) {
			p.Role = g.Role
		}
	}
	return p, p.Role != ""
}

func caller(r *http.Request) auth.Principal { p, _ := principalFrom(r.Context()); return p }

// Shared subnets require the requested role for every associated VLAN.
func (s *Server) subnetAllowed(p auth.Principal, id, min string) bool {
	if p.Global(min) {
		return true
	}
	if id == "" {
		return false
	}
	cfg := s.svc.Config()
	found := false
	for _, sub := range cfg.Subnets {
		if sub.ID == id && sub.VLAN != nil {
			found = true
			if !p.VLAN(*sub.VLAN, min) {
				return false
			}
		}
	}
	for _, v := range cfg.VLANs {
		if v.SubnetRef == id {
			found = true
			if !p.VLAN(v.ID, min) {
				return false
			}
		}
	}
	return found
}

func (s *Server) scopeAllowed(r *http.Request, min string) bool {
	p := caller(r)
	if p.Global(min) {
		return true
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	if r.Method == http.MethodGet {
		switch path {
		case "status", "leases", "subnets", "reservations", "vlans", "parsers/circuit-id", "parsers/circuit-id/profiles":
			return true
		}
	}
	if path == "parsers/circuit-id/test" {
		return true
	}
	root := strings.Split(path, "/")[0]
	var body struct {
		Reservations []config.Reservation `json:"reservations"`
		ID           json.RawMessage      `json:"id"`
		VLAN         *int                 `json:"vlan"`
		SubnetID     string               `json:"subnet_id"`
		SubnetRef    string               `json:"subnet_ref"`
		Interface    string               `json:"interface"`
		Source       string               `json:"source"`
		Match        *config.VLANMatch    `json:"match"`
		Priority     int                  `json:"priority"`
	}
	if r.Method == "POST" || r.Method == "PUT" {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
		if err != nil || len(raw) > 1<<20 {
			return false
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		if json.Unmarshal(raw, &body) != nil {
			return false
		}
	}
	switch root {
	case "vlans":
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if r.Method == "POST" {
			err = json.Unmarshal(body.ID, &id)
		}
		if err != nil || !p.VLAN(id, min) {
			return false
		}
		var old *config.VLAN
		for _, v := range s.svc.Config().VLANs {
			if v.ID == id {
				v := v
				old = &v
			}
		}
		if old != nil && old.SubnetRef != "" && !s.subnetAllowed(p, old.SubnetRef, min) {
			return false
		}
		if r.Method == "GET" || r.Method == "DELETE" {
			return true
		}
		if !s.subnetAllowed(p, body.SubnetRef, min) {
			return false
		}
		// Linux links and packet matching are global infrastructure settings.
		if old == nil {
			return body.Interface == "" && body.Source == "" && body.Match == nil && body.Priority == 0
		}
		a, _ := json.Marshal(old.Match)
		b, _ := json.Marshal(body.Match)
		return old.Interface == body.Interface && old.Source == body.Source && old.Priority == body.Priority && bytes.Equal(a, b)
	case "subnets":
		id := chi.URLParam(r, "id")
		candidateID := id
		if r.Method == "POST" {
			if json.Unmarshal(body.ID, &candidateID) != nil {
				return false
			}
		}
		for _, res := range body.Reservations {
			if res.SubnetID != "" && res.SubnetID != candidateID {
				return false
			}
		}
		if id != "" && !s.subnetAllowed(p, id, min) {
			return false
		}
		if r.Method == "DELETE" || r.Method == "GET" {
			return id != ""
		}
		if body.VLAN == nil || !p.VLAN(*body.VLAN, min) {
			return false
		}
		if r.Method == "POST" {
			if json.Unmarshal(body.ID, &id) != nil {
				return false
			}
			for _, sub := range s.svc.Config().Subnets {
				if sub.ID == id {
					return false
				}
			}
		}
		return true
	case "leases":
		l, err := s.svc.Store().GetLease(r.Context(), chi.URLParam(r, "ip"))
		if err != nil {
			return false
		}
		return s.subnetAllowed(p, l.SubnetID, min) && (l.VLANID == nil || p.VLAN(*l.VLANID, min))
	case "reservations":
		if r.Method == "POST" {
			return s.subnetAllowed(p, body.SubnetID, min)
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			return false
		}
		items, err := s.svc.Store().ListReservations(r.Context())
		if err != nil {
			return false
		}
		for _, item := range items {
			if item.ID == id {
				return s.subnetAllowed(p, item.SubnetID, min)
			}
		}
	}
	return false
}

func (s *Server) allowedSubnets(r *http.Request) []string {
	p := caller(r)
	if p.Global(auth.RoleViewer) {
		return nil
	}
	out := make([]string, 0)
	for _, sub := range s.svc.Config().Subnets {
		if s.subnetAllowed(p, sub.ID, auth.RoleViewer) {
			out = append(out, sub.ID)
		}
	}
	return out
}
func (s *Server) visibleSubnetCount(r *http.Request) int {
	n := 0
	for _, sub := range s.svc.Config().Subnets {
		if s.subnetAllowed(caller(r), sub.ID, auth.RoleViewer) {
			n++
		}
	}
	return n
}
func (s *Server) visibleVLANCount(r *http.Request) int {
	n := 0
	for _, v := range s.svc.Config().VLANs {
		if caller(r).VLAN(v.ID, auth.RoleViewer) && (v.SubnetRef == "" || s.subnetAllowed(caller(r), v.SubnetRef, auth.RoleViewer)) {
			n++
		}
	}
	return n
}
func (s *Server) allowedVLANs(r *http.Request) []int {
	p := caller(r)
	if p.Global(auth.RoleViewer) {
		return nil
	}
	out := make([]int, 0)
	seen := map[int]bool{}
	for _, g := range p.Grants {
		if auth.Rank(g.Role) < 1 {
			continue
		}
		for _, id := range g.VLANIDs {
			if !seen[id] {
				out = append(out, id)
				seen[id] = true
			}
		}
	}
	return out
}

// Serialize configuration mutations before authentication, so a queued request
// cannot restore an older configuration after another request revokes access.
func (s *Server) serializeMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			s.mutationMu.Lock()
			defer s.mutationMu.Unlock()
		}
		next.ServeHTTP(w, r)
	})
}

type leaseView struct {
	model.Lease
	CanOperate bool `json:"can_operate"`
}

func (s *Server) leaseViews(r *http.Request, items []model.Lease) []leaseView {
	out := make([]leaseView, 0, len(items))
	p := caller(r)
	for _, l := range items {
		out = append(out, leaseView{Lease: l, CanOperate: s.subnetAllowed(p, l.SubnetID, auth.RoleOperator) && (l.VLANID == nil || p.VLAN(*l.VLANID, auth.RoleOperator))})
	}
	return out
}

type subnetView struct {
	model.Subnet
	CanAdmin   bool `json:"can_admin"`
	CanOperate bool `json:"can_operate"`
}

func (s *Server) subnetViews(r *http.Request, items []model.Subnet) []subnetView {
	out := make([]subnetView, 0, len(items))
	for _, sub := range items {
		out = append(out, subnetView{Subnet: sub, CanAdmin: s.subnetAllowed(caller(r), sub.ID, auth.RoleAdmin), CanOperate: s.subnetAllowed(caller(r), sub.ID, auth.RoleOperator)})
	}
	return out
}

type reservationView struct {
	model.Reservation
	CanOperate bool `json:"can_operate"`
}

func (s *Server) reservationViews(r *http.Request, items []model.Reservation) []reservationView {
	out := make([]reservationView, 0, len(items))
	for _, res := range items {
		out = append(out, reservationView{Reservation: res, CanOperate: s.subnetAllowed(caller(r), res.SubnetID, auth.RoleOperator)})
	}
	return out
}
