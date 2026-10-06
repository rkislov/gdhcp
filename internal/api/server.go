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

package api

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/kislovrs/godhcp/internal/auth"
	"github.com/kislovrs/godhcp/internal/config"
	"github.com/kislovrs/godhcp/internal/core"
	"github.com/kislovrs/godhcp/internal/logbuf"
	"github.com/kislovrs/godhcp/internal/metrics"
	"github.com/kislovrs/godhcp/internal/model"
	"github.com/kislovrs/godhcp/internal/relay"
	"github.com/kislovrs/godhcp/internal/storage"
	"github.com/kislovrs/godhcp/internal/version"
	"github.com/kislovrs/godhcp/internal/webui"
	"gopkg.in/yaml.v3"
)

//go:embed openapi.yaml
var specFS embed.FS

// Server is the REST and web frontend.
type Server struct {
	svc     *core.Service
	metrics *metrics.Metrics
	logs    *logbuf.Buffer
	started time.Time
	limit   *limiter
}

func New(svc *core.Service, m *metrics.Metrics, logs *logbuf.Buffer) *Server {
	return &Server{svc: svc, metrics: m, logs: logs, started: time.Now(), limit: newLimiter(50)}
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Use(s.rateLimit)
	r.Get("/healthz", s.healthz)
	r.Get("/readyz", s.readyz)
	r.Get("/api/openapi.yaml", s.openapi)
	r.Get("/swagger", s.swagger)
	r.Get("/swagger/", s.swagger)
	r.Post("/api/v1/auth/login", s.login)
	r.Post("/api/v1/auth/refresh", s.refresh)
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.authenticate)
		r.With(s.allow(auth.RoleViewer)).Get("/status", s.status)
		r.With(s.allow(auth.RoleViewer)).Get("/metrics", s.metricsHandler)
		r.With(s.allow(auth.RoleViewer)).Get("/leases", s.listLeases)
		r.With(s.allow(auth.RoleViewer)).Get("/leases/{ip}", s.getLease)
		r.With(s.allow(auth.RoleOperator)).Delete("/leases/{ip}", s.deleteLease)
		r.With(s.allow(auth.RoleOperator)).Post("/leases/{ip}/reserve", s.reserveLease)
		r.With(s.allow(auth.RoleViewer)).Get("/subnets", s.listSubnets)
		r.With(s.allow(auth.RoleAdmin)).Post("/subnets", s.createSubnet)
		r.With(s.allow(auth.RoleAdmin)).Put("/subnets/{id}", s.putSubnet)
		r.With(s.allow(auth.RoleAdmin)).Delete("/subnets/{id}", s.deleteSubnet)
		r.With(s.allow(auth.RoleViewer)).Get("/reservations", s.listReservations)
		r.With(s.allow(auth.RoleOperator)).Post("/reservations", s.createReservation)
		r.With(s.allow(auth.RoleOperator)).Delete("/reservations/{id}", s.deleteReservation)
		r.With(s.allow(auth.RoleViewer)).Get("/vlans", s.listVLANs)
		r.With(s.allow(auth.RoleViewer)).Get("/vlans/{id}", s.getVLAN)
		r.With(s.allow(auth.RoleAdmin)).Post("/vlans", s.createVLAN)
		r.With(s.allow(auth.RoleAdmin)).Put("/vlans/{id}", s.putVLAN)
		r.With(s.allow(auth.RoleAdmin)).Delete("/vlans/{id}", s.deleteVLAN)
		r.With(s.allow(auth.RoleViewer)).Get("/vlans/{id}/leases", s.vlanLeases)
		r.With(s.allow(auth.RoleViewer)).Get("/vlans/{id}/stats", s.vlanStats)
		r.With(s.allow(auth.RoleViewer)).Get("/relays", s.listRelays)
		r.With(s.allow(auth.RoleViewer)).Get("/relays/{giaddr}", s.getRelay)
		r.With(s.allow(auth.RoleAdmin)).Post("/relays", s.createRelay)
		r.With(s.allow(auth.RoleAdmin)).Delete("/relays/{giaddr}", s.deleteRelay)
		r.With(s.allow(auth.RoleViewer)).Get("/relays/{giaddr}/stats", s.relayStats)
		r.With(s.allow(auth.RoleViewer)).Get("/parsers/circuit-id", s.listParsers)
		r.With(s.allow(auth.RoleAdmin)).Post("/parsers/circuit-id", s.createParser)
		r.With(s.allow(auth.RoleOperator)).Post("/parsers/circuit-id/test", s.testParser)
		r.With(s.allow(auth.RoleAdmin)).Get("/config", s.getConfig)
		r.With(s.allow(auth.RoleAdmin)).Put("/config", s.putConfig)
		r.With(s.allow(auth.RoleAdmin)).Post("/config/validate", s.validateConfig)
		r.With(s.allow(auth.RoleOperator)).Post("/config/reload", s.reloadConfig)
		r.With(s.allow(auth.RoleViewer)).Get("/logs/stream", s.streamLogs)
		r.With(s.allow(auth.RoleAdmin)).Get("/users", s.listUsers)
		r.With(s.allow(auth.RoleAdmin)).Post("/users", s.createUser)
		r.With(s.allow(auth.RoleAdmin)).Delete("/users/{username}", s.deleteUser)
	})
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/", http.StatusFound)
	})
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeErr(w, http.StatusNotFound, "not_found", "route not found")
			return
		}
		s.ui(w, r)
	})
	return r
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Store().Ping(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "not_ready", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) openapi(w http.ResponseWriter, _ *http.Request) {
	b, err := specFS.ReadFile("openapi.yaml")
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "openapi spec missing")
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

func (s *Server) swagger(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, swaggerHTML)
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	cfg := s.svc.Config()
	active := 0
	for _, st := range s.svc.PoolStats() {
		active += st.Active
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":        version.Version,
		"author":         version.Author,
		"authoritative":  cfg.Server.Authoritative,
		"uptime_seconds": time.Since(s.started).Seconds(),
		"leases_active":  active,
		"subnets":        len(cfg.Subnets),
		"vlans":          len(cfg.VLANs),
		"database":       cfg.Database.Driver,
	})
}

func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	if s.metrics == nil {
		writeErr(w, http.StatusNotFound, "not_found", "metrics disabled")
		return
	}
	s.metrics.Handler().ServeHTTP(w, r)
}

func (s *Server) listLeases(w http.ResponseWriter, r *http.Request) {
	f := model.LeaseFilter{
		SubnetID: r.URL.Query().Get("subnet"),
		GIAddr:   r.URL.Query().Get("giaddr"),
		Query:    r.URL.Query().Get("q"),
		State:    r.URL.Query().Get("state"),
		Limit:    queryInt(r, "limit", 100),
		Offset:   queryInt(r, "offset", 0),
	}
	if v := r.URL.Query().Get("vlan"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "vlan must be an integer")
			return
		}
		f.VLAN = &n
	}
	items, total, err := s.svc.Store().ListLeases(r.Context(), f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if items == nil {
		items = []model.Lease{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) getLease(w http.ResponseWriter, r *http.Request) {
	l, err := s.svc.Store().GetLease(r.Context(), chi.URLParam(r, "ip"))
	if errors.Is(err, storage.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "lease not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) deleteLease(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteLease(r.Context(), chi.URLParam(r, "ip")); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "lease not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) reserveLease(w http.ResponseWriter, r *http.Request) {
	l, err := s.svc.Store().GetLease(r.Context(), chi.URLParam(r, "ip"))
	if errors.Is(err, storage.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "lease not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	cfg.Reservations = append(cfg.Reservations, config.Reservation{
		MAC: l.MAC, ClientID: l.ClientID, IP: l.IP, Hostname: l.Hostname, SubnetID: l.SubnetID,
	})
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, cfg.Reservations[len(cfg.Reservations)-1])
}

func (s *Server) listSubnets(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.Store().ListSubnets(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if items == nil {
		items = []model.Subnet{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createSubnet(w http.ResponseWriter, r *http.Request) {
	var body config.Subnet
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	cfg.Subnets = append(cfg.Subnets, body)
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, body)
}

func (s *Server) putSubnet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body config.Subnet
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	body.ID = id
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	found := false
	for i := range cfg.Subnets {
		if cfg.Subnets[i].ID == id {
			cfg.Subnets[i] = body
			found = true
			break
		}
	}
	if !found {
		writeErr(w, http.StatusNotFound, "not_found", "subnet not found")
		return
	}
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) deleteSubnet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	next := cfg.Subnets[:0]
	found := false
	for _, sub := range cfg.Subnets {
		if sub.ID == id {
			found = true
			continue
		}
		next = append(next, sub)
	}
	if !found {
		writeErr(w, http.StatusNotFound, "not_found", "subnet not found")
		return
	}
	cfg.Subnets = next
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listReservations(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.Store().ListReservations(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if items == nil {
		items = []model.Reservation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createReservation(w http.ResponseWriter, r *http.Request) {
	var body config.Reservation
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	cfg.Reservations = append(cfg.Reservations, body)
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	items, err := s.svc.Store().ListReservations(r.Context())
	if err != nil || len(items) == 0 {
		writeJSON(w, http.StatusCreated, body)
		return
	}
	writeJSON(w, http.StatusCreated, items[len(items)-1])
}

func (s *Server) deleteReservation(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "id must be an integer")
		return
	}
	items, err := s.svc.Store().ListReservations(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	var target *model.Reservation
	for i := range items {
		if items[i].ID == id {
			target = &items[i]
			break
		}
	}
	if target == nil {
		writeErr(w, http.StatusNotFound, "not_found", "reservation not found")
		return
	}
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	cfg.Reservations = dropReservation(cfg.Reservations, *target)
	for i := range cfg.Subnets {
		cfg.Subnets[i].Reservations = dropReservation(cfg.Subnets[i].Reservations, *target)
	}
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listVLANs(w http.ResponseWriter, _ *http.Request) {
	cfg := s.svc.Config()
	stats := map[string]float64{}
	used := map[string]int{}
	size := map[string]int{}
	for _, st := range s.svc.PoolStats() {
		used[st.Subnet] = st.Active
		size[st.Subnet] = st.Total
		if st.Total > 0 {
			stats[st.Subnet] = float64(st.Active) / float64(st.Total)
		}
	}
	type view struct {
		config.VLAN
		Utilization float64 `json:"utilization"`
		PoolUsed    int     `json:"pool_used"`
		PoolSize    int     `json:"pool_size"`
	}
	items := make([]view, 0, len(cfg.VLANs))
	for _, v := range cfg.VLANs {
		items = append(items, view{VLAN: v, Utilization: stats[v.SubnetRef], PoolUsed: used[v.SubnetRef], PoolSize: size[v.SubnetRef]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getVLAN(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "id must be an integer")
		return
	}
	for _, v := range s.svc.Config().VLANs {
		if v.ID == id {
			writeJSON(w, http.StatusOK, v)
			return
		}
	}
	writeErr(w, http.StatusNotFound, "not_found", "vlan not found")
}

func (s *Server) createVLAN(w http.ResponseWriter, r *http.Request) {
	var body config.VLAN
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	cfg.VLANs = append(cfg.VLANs, body)
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	if cfg.Server.ManageLinks && body.Interface != "" {
		parent := body.Interface
		if i := strings.LastIndex(parent, "."); i > 0 {
			parent = parent[:i]
		}
		if name, err := ensureVLAN(parent, body.ID); err != nil {
			writeJSON(w, http.StatusCreated, map[string]any{"vlan": body, "link_warning": err.Error()})
			return
		} else {
			body.Interface = name
		}
	}
	writeJSON(w, http.StatusCreated, body)
}

func (s *Server) putVLAN(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "id must be an integer")
		return
	}
	var body config.VLAN
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	body.ID = id
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	found := false
	for i := range cfg.VLANs {
		if cfg.VLANs[i].ID == id {
			cfg.VLANs[i] = body
			found = true
			break
		}
	}
	if !found {
		writeErr(w, http.StatusNotFound, "not_found", "vlan not found")
		return
	}
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) deleteVLAN(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "id must be an integer")
		return
	}
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	next := cfg.VLANs[:0]
	found := false
	for _, v := range cfg.VLANs {
		if v.ID == id {
			found = true
			continue
		}
		next = append(next, v)
	}
	if !found {
		writeErr(w, http.StatusNotFound, "not_found", "vlan not found")
		return
	}
	cfg.VLANs = next
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) vlanLeases(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "id must be an integer")
		return
	}
	items, total, err := s.svc.Store().ListLeases(r.Context(), model.LeaseFilter{VLAN: &id, Limit: 1000})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if items == nil {
		items = []model.Lease{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) vlanStats(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "id must be an integer")
		return
	}
	var subnet string
	for _, v := range s.svc.Config().VLANs {
		if v.ID == id {
			subnet = v.SubnetRef
		}
	}
	if subnet == "" {
		writeErr(w, http.StatusNotFound, "not_found", "vlan not found")
		return
	}
	for _, st := range s.svc.PoolStats() {
		if st.Subnet == subnet {
			util := 0.0
			if st.Total > 0 {
				util = float64(st.Active) / float64(st.Total)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"vlan": id, "subnet": subnet, "pool_used": st.Active, "pool_size": st.Total, "utilization": util,
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"vlan": id, "subnet": subnet, "pool_used": 0, "pool_size": 0, "utilization": 0})
}

func (s *Server) listRelays(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.Store().ListRelays(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if items == nil {
		items = []model.Relay{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getRelay(w http.ResponseWriter, r *http.Request) {
	gi := chi.URLParam(r, "giaddr")
	items, err := s.svc.Store().ListRelays(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	for _, item := range items {
		if item.GIAddr == gi {
			writeJSON(w, http.StatusOK, item)
			return
		}
	}
	writeErr(w, http.StatusNotFound, "not_found", "relay not found")
}

func (s *Server) createRelay(w http.ResponseWriter, r *http.Request) {
	var body config.TrustedRelay
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	cfg.Relay.TrustedRelays = append(cfg.Relay.TrustedRelays, body)
	cfg.Relay.Enabled = true
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, body)
}

func (s *Server) deleteRelay(w http.ResponseWriter, r *http.Request) {
	gi := chi.URLParam(r, "giaddr")
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	next := cfg.Relay.TrustedRelays[:0]
	found := false
	for _, t := range cfg.Relay.TrustedRelays {
		if t.GIAddr == gi {
			found = true
			continue
		}
		next = append(next, t)
	}
	if !found {
		writeErr(w, http.StatusNotFound, "not_found", "relay not found")
		return
	}
	cfg.Relay.TrustedRelays = next
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) relayStats(w http.ResponseWriter, r *http.Request) {
	gi := chi.URLParam(r, "giaddr")
	stats := s.svc.RelayStats(gi)
	if len(stats) == 0 {
		writeJSON(w, http.StatusOK, model.RelayStats{GIAddr: gi})
		return
	}
	writeJSON(w, http.StatusOK, stats[0])
}

func (s *Server) listParsers(w http.ResponseWriter, _ *http.Request) {
	cfg := s.svc.Config()
	writeJSON(w, http.StatusOK, map[string]any{"items": cfg.Relay.CircuitIDParsers})
}

func (s *Server) createParser(w http.ResponseWriter, r *http.Request) {
	var body config.Parser
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	cfg.Relay.CircuitIDParsers = append(cfg.Relay.CircuitIDParsers, body)
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, body)
}

func (s *Server) testParser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Parser    string `json:"parser"`
		CircuitID string `json:"circuit_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	var custom []relay.Parser
	for _, p := range s.svc.Config().Relay.CircuitIDParsers {
		compiled, err := relay.Compile(p.Name, p.Regex, p.VLANGroup)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
			return
		}
		custom = append(custom, compiled)
	}
	parsers := relay.MergeParsers(custom)
	vlan, name, fields, ok := relay.MatchVLAN(parsers, body.CircuitID, body.Parser)
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "vlan": vlan, "parser": name, "fields": fields})
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.svc.Config()
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, http.StatusOK, redact(cfg))
		return
	}
	raw, err := yaml.Marshal(redact(cfg))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	cfg, err := decodeConfig(body, r.Header.Get("Content-Type"), s.svc.Config().Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	restoreSecrets(cfg, s.svc.Config())
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
}

func (s *Server) validateConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	cfg, err := decodeConfig(body, r.Header.Get("Content-Type"), "")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "valid"})
}

func (s *Server) reloadConfig(w http.ResponseWriter, r *http.Request) {
	path := s.svc.Config().Path
	if path == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "config path is empty")
		return
	}
	cfg, err := config.Load(path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	if err := s.svc.Apply(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reloaded"})
}

func (s *Server) streamLogs(w http.ResponseWriter, r *http.Request) {
	if s.logs == nil {
		writeErr(w, http.StatusNotFound, "not_found", "log buffer disabled")
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "internal", "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	for _, line := range s.logs.Recent() {
		_, _ = io.WriteString(w, "data: "+line+"\n\n")
	}
	fl.Flush()
	ch, cancel := s.logs.Subscribe()
	defer cancel()
	for {
		select {
		case <-r.Context().Done():
			return
		case line := <-ch:
			_, _ = io.WriteString(w, "data: "+line+"\n\n")
			fl.Flush()
		}
	}
}

func (s *Server) listUsers(w http.ResponseWriter, _ *http.Request) {
	users := s.svc.Config().API.Auth.Users
	out := make([]map[string]string, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]string{"username": u.Username, "role": u.Role})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	cfg.API.Auth.Users = append(cfg.API.Auth.Users, config.User{Username: body.Username, PasswordHash: hash, Role: body.Role})
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"username": body.Username, "role": body.Role})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "username")
	cfg, err := cloneConfig(s.svc.Config())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	next := cfg.API.Auth.Users[:0]
	found := false
	for _, u := range cfg.API.Auth.Users {
		if u.Username == name {
			found = true
			continue
		}
		next = append(next, u)
	}
	if !found {
		writeErr(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	cfg.API.Auth.Users = next
	if err := s.commit(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	cfg := s.svc.Config()
	var user *config.User
	for i := range cfg.API.Auth.Users {
		if cfg.API.Auth.Users[i].Username == body.Username {
			user = &cfg.API.Auth.Users[i]
			break
		}
	}
	if user == nil || !auth.VerifyPassword(body.Password, user.PasswordHash) {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid credentials")
		return
	}
	iss := s.issuer()
	if iss == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "jwt secret is not configured")
		return
	}
	tok, err := iss.Issue(user.Username, user.Role)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tok)
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	iss := s.issuer()
	if iss == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "jwt secret is not configured")
		return
	}
	p, err := iss.Parse(body.RefreshToken, "refresh")
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid refresh token")
		return
	}
	tok, err := iss.Issue(p.Name, p.Role)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tok)
}

func (s *Server) ui(w http.ResponseWriter, r *http.Request) {
	cfg := s.svc.Config()
	if cfg != nil && !cfg.Web.Enabled {
		http.NotFound(w, r)
		return
	}
	prefix := "/ui"
	if cfg != nil && cfg.Web.Path != "" {
		prefix = strings.TrimRight(cfg.Web.Path, "/")
	}
	webui.Handler(prefix).ServeHTTP(w, r)
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.svc.Config()
		if len(cfg.API.Auth.Users) == 0 && len(cfg.API.Auth.APIKeys) == 0 {
			next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), auth.Principal{Name: "anonymous", Role: auth.RoleAdmin})))
			return
		}
		if key := r.Header.Get("X-API-Key"); key != "" {
			for _, k := range cfg.API.Auth.APIKeys {
				if len(k.Key) == len(key) && subtle.ConstantTimeCompare([]byte(k.Key), []byte(key)) == 1 {
					next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), auth.Principal{Name: k.Name, Role: k.Role})))
					return
				}
			}
			writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid api key")
			return
		}
		h := r.Header.Get("Authorization")
		token := strings.TrimPrefix(h, "Bearer ")
		if token == "" || token == h {
			if q := r.URL.Query().Get("access_token"); q != "" {
				token = q
			}
		}
		iss := s.issuer()
		if iss == nil || token == "" {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		p, err := iss.Parse(token, "access")
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid token")
			return
		}
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
	})
}

func (s *Server) allow(min string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, _ := principalFrom(r.Context())
			if !auth.Allows(p.Role, min) {
				writeErr(w, http.StatusForbidden, "forbidden", "insufficient role")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) issuer() *auth.Issuer {
	cfg := s.svc.Config()
	if strings.TrimSpace(cfg.API.Auth.JWTSecret) == "" {
		return nil
	}
	return auth.NewIssuer(cfg.API.Auth.JWTSecret, cfg.API.Auth.TokenTTL, cfg.API.Auth.RefreshTTL)
}

func (s *Server) commit(ctx context.Context, cfg *config.Config) error {
	if err := s.svc.Apply(ctx, cfg); err != nil {
		return err
	}
	if cfg.Path != "" {
		if err := config.Save(cfg.Path, cfg); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if host == "" {
			host = r.RemoteAddr
		}
		limit := 50.0
		if cfg := s.svc.Config(); cfg != nil && cfg.API.RateLimit > 0 {
			limit = cfg.API.RateLimit
		}
		if !s.limit.allow(host, limit) {
			writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type principalKey struct{}

func withPrincipal(ctx context.Context, p auth.Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func principalFrom(ctx context.Context) (auth.Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(auth.Principal)
	return p, ok
}

func cloneConfig(cfg *config.Config) (*config.Config, error) {
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var out config.Config
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	out.Path = cfg.Path
	return &out, nil
}

func decodeConfig(body []byte, contentType, path string) (*config.Config, error) {
	if strings.Contains(contentType, "json") {
		var cfg config.Config
		if err := json.Unmarshal(body, &cfg); err != nil {
			return nil, err
		}
		cfg.Path = path
		return &cfg, nil
	}
	return config.Parse(body, path)
}

func restoreSecrets(next, prev *config.Config) {
	if next == nil || prev == nil {
		return
	}
	if next.API.Auth.JWTSecret == "***" {
		next.API.Auth.JWTSecret = prev.API.Auth.JWTSecret
	}
	for i := range next.API.Auth.APIKeys {
		if next.API.Auth.APIKeys[i].Key != "***" {
			continue
		}
		for _, old := range prev.API.Auth.APIKeys {
			if old.Name == next.API.Auth.APIKeys[i].Name {
				next.API.Auth.APIKeys[i].Key = old.Key
			}
		}
	}
}

func redact(cfg *config.Config) *config.Config {
	out, err := cloneConfig(cfg)
	if err != nil {
		return cfg
	}
	if out.API.Auth.JWTSecret != "" {
		out.API.Auth.JWTSecret = "***"
	}
	for i := range out.API.Auth.APIKeys {
		out.API.Auth.APIKeys[i].Key = "***"
	}
	return out
}

func dropReservation(list []config.Reservation, target model.Reservation) []config.Reservation {
	next := list[:0]
	for _, r := range list {
		if r.IP == target.IP && r.SubnetID == target.SubnetID {
			continue
		}
		next = append(next, r)
	}
	return next
}

func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, errCode, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]string{"code": errCode, "message": msg}})
}

type limiter struct {
	mu   sync.Mutex
	hits map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(rate float64) *limiter {
	return &limiter{hits: map[string]*bucket{}}
}

func (l *limiter) allow(key string, rate float64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b := l.hits[key]
	if b == nil {
		l.hits[key] = &bucket{tokens: rate - 1, last: now}
		return true
	}
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * rate
	if b.tokens > rate {
		b.tokens = rate
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func ensureVLAN(parent string, id int) (string, error) {
	return vlanEnsure(parent, id)
}

const swaggerHTML = `<!DOCTYPE html>
<html lang="ru"><head><meta charset="utf-8"><title>GoDHCP API</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css"></head>
<body><div id="swagger"></div>
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>SwaggerUIBundle({url:"/api/openapi.yaml",dom_id:"#swagger"})</script>
</body></html>`
