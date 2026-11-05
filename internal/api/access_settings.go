// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/go-ldap/ldap/v3"
	"github.com/kislovrs/godhcp/internal/auth"
	"github.com/kislovrs/godhcp/internal/config"
	"net/http"
)

func hasGlobalAdmin(cfg *config.Config) bool {
	if hasLocalGlobalAdmin(cfg) {
		return true
	}
	for _, k := range cfg.API.Auth.APIKeys {
		if k.Role == auth.RoleAdmin && len(k.VLANIDs) == 0 {
			return true
		}
	}
	return false
}
func hasLocalGlobalAdmin(cfg *config.Config) bool {
	for _, u := range cfg.API.Auth.Users {
		if u.PasswordHash == "" {
			continue
		}
		if u.Role == auth.RoleAdmin && len(u.VLANIDs) == 0 {
			return true
		}
		for _, name := range u.Groups {
			for _, g := range cfg.API.Auth.Groups {
				if g.Name == name && g.Role == auth.RoleAdmin && len(g.VLANIDs) == 0 {
					return true
				}
			}
		}
	}
	return false
}
func (s *Server) getAccess(w http.ResponseWriter, r *http.Request) {
	cfg := redact(s.svc.Config())
	groups := cfg.API.Auth.Groups
	if groups == nil {
		groups = []config.AccessGroup{}
	}
	writeJSON(w, 200, map[string]any{"ldap": cfg.API.Auth.LDAP, "groups": groups, "vlans": cfg.VLANs})
}
func (s *Server) putLDAP(w http.ResponseWriter, r *http.Request) {
	var body config.LDAP
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad_request", err.Error())
		return
	}
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	if body.BindPassword == "***" {
		body.BindPassword = cfg.API.Auth.LDAP.BindPassword
	}
	cfg.API.Auth.LDAP = body
	if !hasLocalGlobalAdmin(cfg) {
		writeErr(w, 409, "conflict", "configure a local global administrator first")
		return
	}
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, 400, "invalid_config", err.Error())
		return
	}
	s.getAccess(w, r)
}
func (s *Server) testLDAP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LDAP     config.LDAP `json:"ldap"`
		Username string      `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad_request", err.Error())
		return
	}
	if body.LDAP.BindPassword == "***" {
		body.LDAP.BindPassword = s.svc.Config().API.Auth.LDAP.BindPassword
	}
	groups, err := auth.Directory(r.Context(), body.LDAP, body.Username, nil)
	if err != nil {
		writeErr(w, 400, "ldap_failed", "LDAP lookup failed: check TLS certificate, bind account, base DN and user filter")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "groups": groups})
}
func (s *Server) putAccessGroup(w http.ResponseWriter, r *http.Request) {
	var body config.AccessGroup
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad_request", err.Error())
		return
	}
	if body.LDAPDN != "" {
		if _, err := ldap.ParseDN(body.LDAPDN); err != nil {
			writeErr(w, 400, "invalid_dn", err.Error())
			return
		}
	}
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	found := false
	for i, g := range cfg.API.Auth.Groups {
		if g.Name == body.Name {
			cfg.API.Auth.Groups[i] = body
			found = true
			break
		}
	}
	if !found {
		cfg.API.Auth.Groups = append(cfg.API.Auth.Groups, body)
	}
	if !hasGlobalAdmin(cfg) {
		writeErr(w, 409, "conflict", "keep a local global administrator")
		return
	}
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, 400, "invalid_config", err.Error())
		return
	}
	writeJSON(w, 200, body)
}
func (s *Server) deleteAccessGroup(w http.ResponseWriter, r *http.Request) {
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	name := chi.URLParam(r, "name")
	next := cfg.API.Auth.Groups[:0]
	found := false
	for _, g := range cfg.API.Auth.Groups {
		if g.Name == name {
			found = true
		} else {
			next = append(next, g)
		}
	}
	if !found {
		writeErr(w, 404, "not_found", "group not found")
		return
	}
	cfg.API.Auth.Groups = next
	if !hasGlobalAdmin(cfg) {
		writeErr(w, 409, "conflict", "keep a local global administrator")
		return
	}
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, 400, "invalid_config", err.Error())
		return
	}
	w.WriteHeader(204)
}
