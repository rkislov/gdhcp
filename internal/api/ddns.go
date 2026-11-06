// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"github.com/kislovrs/godhcp/internal/config"
	"github.com/kislovrs/godhcp/internal/ddns"
	"net/http"
)

func (s *Server) getDDNS(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.svc.Config().DDNS)
}
func (s *Server) putDDNS(w http.ResponseWriter, r *http.Request) {
	var body config.DDNS
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad_request", err.Error())
		return
	}
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	cfg.DDNS = body
	cfg.ApplyDefaults()
	if err := config.ValidateDDNS(cfg.DDNS); err != nil {
		writeErr(w, 400, "invalid_ddns", err.Error())
		return
	}
	if cfg.DDNS.Enabled {
		if err := ddns.CheckTools(cfg.DDNS); err != nil {
			writeErr(w, 400, "ddns_tools_missing", err.Error())
			return
		}
	}
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, 400, "invalid_config", err.Error())
		return
	}
	s.getDDNS(w, r)
}
