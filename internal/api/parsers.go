// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"github.com/go-chi/chi/v5"
	"github.com/kislovrs/godhcp/internal/relay"
	"net/http"
)

func (s *Server) parserProfiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": relay.Profiles()})
}

func (s *Server) deleteParser(w http.ResponseWriter, r *http.Request) {
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	name := chi.URLParam(r, "name")
	next := cfg.Relay.CircuitIDParsers[:0]
	found := false
	for _, p := range cfg.Relay.CircuitIDParsers {
		if p.Name == name {
			found = true
		} else {
			next = append(next, p)
		}
	}
	if !found {
		writeErr(w, 404, "not_found", "custom parser not found; built-in parsers cannot be deleted")
		return
	}
	cfg.Relay.CircuitIDParsers = next
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, 400, "invalid_config", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
