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
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/kislovrs/godhcp/internal/auth"
	"github.com/kislovrs/godhcp/internal/config"
	"github.com/kislovrs/godhcp/internal/core"
	"github.com/kislovrs/godhcp/internal/logbuf"
	"github.com/kislovrs/godhcp/internal/metrics"
	"github.com/kislovrs/godhcp/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestHTTP(t *testing.T) {
	h := newAPI(t)
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	t.Run("health", func(t *testing.T) {
		res := get(t, ts.URL+"/healthz", "")
		require.Equal(t, http.StatusOK, res.StatusCode)
		res = get(t, ts.URL+"/readyz", "")
		require.Equal(t, http.StatusOK, res.StatusCode)
	})

	t.Run("openapi and ui", func(t *testing.T) {
		res := get(t, ts.URL+"/api/openapi.yaml", "")
		require.Equal(t, http.StatusOK, res.StatusCode)
		body, _ := io.ReadAll(res.Body)
		require.Contains(t, string(body), "openapi: 3.1.0")
		res = get(t, ts.URL+"/swagger", "")
		require.Equal(t, http.StatusOK, res.StatusCode)
		res = get(t, ts.URL+"/ui/", "")
		require.Equal(t, http.StatusOK, res.StatusCode)
		html, _ := io.ReadAll(res.Body)
		require.Contains(t, string(html), "GoDHCP")
	})

	t.Run("login and rbac", func(t *testing.T) {
		res := postJSON(t, ts.URL+"/api/v1/auth/login", "", map[string]string{"username": "admin", "password": "nope"})
		require.Equal(t, http.StatusUnauthorized, res.StatusCode)

		res = postJSON(t, ts.URL+"/api/v1/auth/login", "", map[string]string{"username": "admin", "password": "secret"})
		require.Equal(t, http.StatusOK, res.StatusCode)
		var tok auth.Tokens
		require.NoError(t, json.NewDecoder(res.Body).Decode(&tok))
		require.NotEmpty(t, tok.AccessToken)
		require.Equal(t, auth.RoleAdmin, tok.Role)

		res = get(t, ts.URL+"/api/v1/status", tok.AccessToken)
		require.Equal(t, http.StatusOK, res.StatusCode)

		res = postJSON(t, ts.URL+"/api/v1/auth/login", "", map[string]string{"username": "view", "password": "secret"})
		require.Equal(t, http.StatusOK, res.StatusCode)
		var view auth.Tokens
		require.NoError(t, json.NewDecoder(res.Body).Decode(&view))
		res = postJSON(t, ts.URL+"/api/v1/subnets", view.AccessToken, map[string]any{"id": "x"})
		require.Equal(t, http.StatusForbidden, res.StatusCode)

		res = get(t, ts.URL+"/api/v1/vlans", tok.AccessToken)
		require.Equal(t, http.StatusOK, res.StatusCode)
		res = get(t, ts.URL+"/api/v1/subnets", tok.AccessToken)
		require.Equal(t, http.StatusOK, res.StatusCode)
		res = get(t, ts.URL+"/api/v1/leases?vlan=10", tok.AccessToken)
		require.Equal(t, http.StatusOK, res.StatusCode)
	})

	t.Run("reservation and circuit id", func(t *testing.T) {
		res := postJSON(t, ts.URL+"/api/v1/auth/login", "", map[string]string{"username": "admin", "password": "secret"})
		var tok auth.Tokens
		require.NoError(t, json.NewDecoder(res.Body).Decode(&tok))

		res = postJSON(t, ts.URL+"/api/v1/reservations", tok.AccessToken, map[string]string{
			"mac": "02:00:00:00:00:99", "ip": "192.168.10.51", "hostname": "desk", "subnet_id": "office-lan",
		})
		require.Equal(t, http.StatusCreated, res.StatusCode)

		res = postJSON(t, ts.URL+"/api/v1/parsers/circuit-id/test", tok.AccessToken, map[string]string{
			"parser": "cisco", "circuit_id": "Gi0/1:vlan20",
		})
		require.Equal(t, http.StatusOK, res.StatusCode)
		var parsed map[string]any
		require.NoError(t, json.NewDecoder(res.Body).Decode(&parsed))
		require.Equal(t, true, parsed["ok"])
		require.EqualValues(t, 20, parsed["vlan"])
	})
}

func newAPI(t *testing.T) http.Handler {
	t.Helper()
	ctx := context.Background()
	raw := []byte(`
server:
  authoritative: true
  ping_check: false
  lease_default: 1h
  lease_max: 2h
  server_id: 192.168.10.1
database:
  driver: sqlite
  dsn: unused
api:
  listen: ":0"
  auth:
    jwt_secret: "dev-secret-dev-secret-dev-secret"
web:
  enabled: true
  path: /ui
vlans:
  - id: 10
    name: office
    interface: eth0.10
    priority: 5
    subnet_ref: office-lan
subnets:
  - id: office-lan
    network: 192.168.10.0/24
    range: [192.168.10.100, 192.168.10.200]
    gateway: 192.168.10.1
    vlan: 10
relay:
  enabled: true
  max_relay_hops: 4
  unknown_vlan_action: ignore
logging:
  level: info
  format: json
`)
	cfg, err := config.Parse(raw, "")
	require.NoError(t, err)
	hash, err := auth.HashPassword("secret")
	require.NoError(t, err)
	cfg.API.Auth.Users = []config.User{
		{Username: "admin", PasswordHash: hash, Role: auth.RoleAdmin},
		{Username: "view", PasswordHash: hash, Role: auth.RoleViewer},
	}
	dsn := filepath.Join(t.TempDir(), "leases.db")
	cfg.Database.DSN = dsn
	store, err := storage.Open(ctx, "sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	svc, err := core.New(ctx, core.Options{
		Store: store, Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
	return New(svc, metrics.New(), logbuf.New(32)).Handler()
}

func get(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func postJSON(t *testing.T, url, token string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}
