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
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kislovrs/godhcp/internal/config"
	"github.com/kislovrs/godhcp/internal/core"
	"github.com/kislovrs/godhcp/internal/metrics"
	"github.com/kislovrs/godhcp/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestStatusLeasesAndParser(t *testing.T) {
	ctx := context.Background()
	cfg, err := config.Parse([]byte(`
server:
  authoritative: true
  lease_default: 1h
  lease_max: 2h
  server_id: 192.168.10.1
database:
  driver: sqlite
  dsn: /tmp/unused.db
subnets:
  - id: office-lan
    network: 192.168.10.0/24
    range: [192.168.10.100, 192.168.10.120]
    gateway: 192.168.10.1
    vlan: 10
vlans:
  - id: 10
    name: office
    priority: 5
    subnet_ref: office-lan
logging:
  level: info
  format: json
`), "mem.yml")
	require.NoError(t, err)
	dir := t.TempDir()
	dsn := filepath.Join(dir, "leases.db")
	store, err := storage.Open(ctx, "sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	m := metrics.New()
	svc, err := core.New(ctx, core.Options{
		Store: store, Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Metrics: m,
	})
	require.NoError(t, err)
	ts := httptest.NewServer(New(svc, m, nil).Handler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/healthz")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)
	res.Body.Close()

	res, err = http.Get(ts.URL + "/api/v1/status")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)
	var status map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&status))
	res.Body.Close()
	require.Equal(t, true, status["authoritative"])

	res, err = http.Post(ts.URL+"/api/v1/parsers/circuit-id/test", "application/json", strings.NewReader(`{"parser":"cisco","circuit_id":"Gi0/1:vlan20"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)
	var parsed map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&parsed))
	res.Body.Close()
	require.Equal(t, true, parsed["ok"])
	require.Equal(t, float64(20), parsed["vlan"])
}
