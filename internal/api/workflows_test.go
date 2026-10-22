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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kislovrs/godhcp/internal/auth"
	"github.com/kislovrs/godhcp/internal/config"
	"github.com/kislovrs/godhcp/internal/core"
	"github.com/kislovrs/godhcp/internal/logbuf"
	"github.com/kislovrs/godhcp/internal/metrics"
	"github.com/kislovrs/godhcp/internal/model"
	"github.com/kislovrs/godhcp/internal/storage"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func workflowAPI(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	ctx := context.Background()
	cfg, err := config.Load("../../configs/dev.yml")
	require.NoError(t, err)
	cfg.Path = filepath.Join(t.TempDir(), "config.yml")
	cfg.Database.DSN = filepath.Join(t.TempDir(), "leases.db")
	cfg.API.Auth.Users = nil
	cfg.API.Auth.APIKeys = []config.APIKey{{Name: "admin", Key: "test-admin", Role: "admin"}, {Name: "view", Key: "test-view", Role: "viewer"}, {Name: "operator", Key: "test-operator", Role: "operator"}}
	cfg.API.RateLimit = 10000
	require.NoError(t, config.Save(cfg.Path, cfg))
	st, err := storage.Open(ctx, "sqlite", cfg.Database.DSN)
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	svc, err := core.New(ctx, core.Options{Store: st, Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	require.NoError(t, err)
	s := New(svc, metrics.New(), logbuf.New(10))
	return s, s.Handler()
}

func callAPI(t *testing.T, h http.Handler, method, path, body string, status int, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, status, rec.Code, "%s %s: %s", method, path, rec.Body.String())
	return rec
}

func TestCatalogHTTPWorkflows(t *testing.T) {
	_, h := workflowAPI(t)
	call := func(method, path, body string, status int) *httptest.ResponseRecorder {
		return callAPI(t, h, method, "/api/v1"+path, body, status, "test-admin")
	}
	call("POST", "/subnets", `{"id":"lab","network":"10.90.0.0/24","range":["10.90.0.10","10.90.0.20"],"gateway":"10.90.0.1"}`, 201)
	rec := call("GET", "/subnets", "", 200)
	require.Contains(t, rec.Body.String(), "10.90.0.0/24")
	call("PUT", "/subnets/lab", `{"network":"10.90.0.0/24","range":["10.90.0.10","10.90.0.30"],"gateway":"10.90.0.1"}`, 200)
	call("POST", "/vlans", `{"id":90,"name":"lab","priority":3,"subnet_ref":"lab"}`, 201)
	call("PUT", "/vlans/90", `{"name":"lab-updated","priority":4,"subnet_ref":"lab"}`, 200)
	require.Contains(t, call("GET", "/vlans/90", "", 200).Body.String(), "lab-updated")
	call("GET", "/vlans/90/stats", "", 200)
	call("GET", "/vlans/90/leases", "", 200)
	call("POST", "/reservations", `{"mac":"02:00:00:00:90:01","ip":"10.90.0.5","subnet_id":"lab"}`, 201)
	rec = call("GET", "/reservations", "", 200)
	var rs struct {
		Items []model.Reservation `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rs))
	var id int64
	for _, r := range rs.Items {
		if r.IP == "10.90.0.5" {
			id = r.ID
		}
	}
	require.Positive(t, id)
	call("DELETE", fmt.Sprintf("/reservations/%d", id), "", 204)
	call("POST", "/relays", `{"giaddr":"10.90.0.1","remote_id":"lab-relay","vlan":90}`, 201)
	require.Contains(t, call("GET", "/relays", "", 200).Body.String(), "lab-relay")
	call("GET", "/relays/10.90.0.1", "", 200)
	call("GET", "/relays/10.90.0.1/stats", "", 200)
	call("DELETE", "/relays/10.90.0.1", "", 204)
	call("DELETE", "/vlans/90", "", 204)
	call("DELETE", "/subnets/lab", "", 204)
	call("GET", "/vlans/90", "", 404)
	call("POST", "/parsers/circuit-id", `{"name":"lab","regex":"^port-(?P<vlan>[0-9]+)$","vlan_group":"vlan"}`, 201)
	call("GET", "/parsers/circuit-id", "", 200)
	require.Contains(t, call("POST", "/parsers/circuit-id/test", `{"parser":"lab","circuit_id":"port-90"}`, 200).Body.String(), `"vlan":90`)
	require.Contains(t, call("POST", "/parsers/circuit-id/test", `{"parser":"lab","circuit_id":"broken"}`, 200).Body.String(), `"ok":false`)
}

func TestLeaseHTTPWorkflow(t *testing.T) {
	s, h := workflowAPI(t)
	vlan := 10
	l := model.Lease{IP: "192.168.10.110", MAC: "02:00:00:00:10:01", Hostname: "desk", SubnetID: "office-lan", VLANID: &vlan, State: "bound", ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, s.svc.Store().UpsertLease(context.Background(), l))
	for _, path := range []string{"/leases/" + l.IP, "/leases?vlan=10&subnet=office-lan&state=bound&q=desk&limit=1&offset=0", "/vlans/10/leases", "/vlans/10/stats"} {
		r := callAPI(t, h, "GET", "/api/v1"+path, "", 200, "test-view")
		require.NotEmpty(t, r.Body.String())
	}
	callAPI(t, h, "POST", "/api/v1/leases/"+l.IP+"/reserve", "", 201, "test-operator")
	rs, err := s.svc.Store().ListReservations(context.Background())
	require.NoError(t, err)
	found := false
	for _, r := range rs {
		if r.IP == l.IP {
			found = true
		}
	}
	require.True(t, found)
	callAPI(t, h, "DELETE", "/api/v1/leases/"+l.IP, "", 204, "test-operator")
	callAPI(t, h, "GET", "/api/v1/leases/"+l.IP, "", 404, "test-view")
	callAPI(t, h, "DELETE", "/api/v1/leases/"+l.IP, "", 404, "test-operator")
	callAPI(t, h, "POST", "/api/v1/leases/"+l.IP+"/reserve", "", 404, "test-operator")
}

func TestHTTPValidationAndNotFound(t *testing.T) {
	_, h := workflowAPI(t)
	for _, tc := range []struct {
		m, p, b string
		status  int
	}{
		{"GET", "/leases?vlan=no", "", 400}, {"GET", "/leases?limit=no&offset=no", "", 200},
		{"GET", "/vlans/no", "", 400}, {"GET", "/vlans/no/stats", "", 400}, {"GET", "/vlans/no/leases", "", 400},
		{"PUT", "/vlans/no", "{}", 400}, {"DELETE", "/vlans/no", "", 400}, {"DELETE", "/reservations/no", "", 400},
		{"GET", "/vlans/999", "", 404}, {"GET", "/vlans/999/stats", "", 404}, {"DELETE", "/vlans/999", "", 404},
		{"PUT", "/vlans/999", "{}", 404}, {"PUT", "/subnets/missing", "{}", 404}, {"DELETE", "/subnets/missing", "", 404},
		{"GET", "/relays/missing", "", 404}, {"DELETE", "/relays/missing", "", 404}, {"DELETE", "/reservations/999", "", 404},
		{"DELETE", "/users/missing", "", 404}, {"GET", "/missing", "", 404},
		{"POST", "/subnets", "{}", 400}, {"POST", "/vlans", "{}", 400}, {"POST", "/reservations", "{}", 400},
		{"POST", "/relays", `{"giaddr":"broken"}`, 400}, {"POST", "/parsers/circuit-id", `{"name":"bad","regex":"["}`, 400},
		{"DELETE", "/subnets/office-lan", "", 409},
	} {
		t.Run(tc.m+tc.p, func(t *testing.T) { callAPI(t, h, tc.m, "/api/v1"+tc.p, tc.b, tc.status, "test-admin") })
	}
	for _, path := range []string{"/subnets", "/reservations", "/vlans", "/relays", "/users", "/parsers/circuit-id", "/parsers/circuit-id/test", "/auth/login", "/auth/refresh"} {
		callAPI(t, h, "POST", "/api/v1"+path, "{", 400, "test-admin")
	}
	for _, path := range []string{"/subnets/office-lan", "/vlans/10"} {
		callAPI(t, h, "PUT", "/api/v1"+path, "{", 400, "test-admin")
	}
}

func TestConfigHTTPRoundTripAndReload(t *testing.T) {
	s, h := workflowAPI(t)
	req := httptest.NewRequest("GET", "/api/v1/config", nil)
	req.Header.Set("X-API-Key", "test-admin")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code)
	require.NotContains(t, rec.Body.String(), "test-admin")
	var cfg config.Config
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &cfg))
	cfg.Logging.Level = "debug"
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	callAPI(t, h, "PUT", "/api/v1/config", string(raw), 200, "test-admin")
	require.Equal(t, "debug", s.svc.Config().Logging.Level)
	require.Equal(t, "test-admin", s.svc.Config().API.Auth.APIKeys[0].Key)
	disk, err := config.Load(s.svc.Config().Path)
	require.NoError(t, err)
	require.Equal(t, "debug", disk.Logging.Level)
	callAPI(t, h, "POST", "/api/v1/config/validate", string(raw), 200, "test-admin")
	callAPI(t, h, "POST", "/api/v1/config/validate", `{"server":{"lease_default":-1}}`, 400, "test-admin")
	callAPI(t, h, "PUT", "/api/v1/config", "{", 400, "test-admin")
	yamlBody := callAPI(t, h, "GET", "/api/v1/config", "", 200, "test-admin").Body.String()
	require.Contains(t, yamlBody, "***")
	req = httptest.NewRequest("PUT", "/api/v1/config", strings.NewReader(yamlBody))
	req.Header.Set("X-API-Key", "test-admin")
	req.Header.Set("Content-Type", "application/yaml")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	callAPI(t, h, "POST", "/api/v1/config/reload", "", 200, "test-admin")
	require.NoError(t, os.WriteFile(s.svc.Config().Path, []byte("server: ["), 0600))
	callAPI(t, h, "POST", "/api/v1/config/reload", "", 400, "test-admin")
	require.Equal(t, "debug", s.svc.Config().Logging.Level)
	// Restore file and ensure YAML validation works without changing active config.
	raw, err = yaml.Marshal(s.svc.Config())
	require.NoError(t, err)
	req = httptest.NewRequest("POST", "/api/v1/config/validate", bytes.NewReader(raw))
	req.Header.Set("X-API-Key", "test-admin")
	req.Header.Set("Content-Type", "application/yaml")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code)
}

func TestUsersTokensAndPermissions(t *testing.T) {
	s, h := workflowAPI(t)
	callAPI(t, h, "POST", "/api/v1/users", `{"username":"alice","password":"secret","role":"operator"}`, 201, "test-admin")
	rec := callAPI(t, h, "GET", "/api/v1/users", "", 200, "test-admin")
	require.NotContains(t, rec.Body.String(), "argon2")
	require.Contains(t, rec.Body.String(), "alice")
	rec = callAPI(t, h, "POST", "/api/v1/auth/login", `{"username":"alice","password":"secret"}`, 200, "")
	var tok auth.Tokens
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &tok))
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	require.Equal(t, 200, r.Code)
	callAPI(t, h, "GET", "/api/v1/status?access_token="+tok.AccessToken, "", 200, "")
	raw, _ := json.Marshal(map[string]string{"refresh_token": tok.RefreshToken})
	callAPI(t, h, "POST", "/api/v1/auth/refresh", string(raw), 200, "")
	callAPI(t, h, "POST", "/api/v1/auth/refresh", `{"refresh_token":"bad"}`, 401, "")
	callAPI(t, h, "GET", "/api/v1/status", "", 401, "bad-key")
	callAPI(t, h, "GET", "/api/v1/status", "", 401, "")
	callAPI(t, h, "POST", "/api/v1/subnets", "{}", 403, "test-view")
	req = httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer broken")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	require.Equal(t, 401, r.Code)
	callAPI(t, h, "DELETE", "/api/v1/users/alice", "", 204, "test-admin")
	callAPI(t, h, "POST", "/api/v1/auth/login", `{"username":"alice","password":"secret"}`, 401, "")
	cfg := s.svc.Config()
	cfg.API.Auth.JWTSecret = ""
	require.NoError(t, s.svc.Apply(context.Background(), cfg))
	callAPI(t, h, "POST", "/api/v1/auth/refresh", `{"refresh_token":"bad"}`, 401, "")
}

func TestClosedDatabaseHTTPFailures(t *testing.T) {
	s, h := workflowAPI(t)
	require.NoError(t, s.svc.Store().Close())
	callAPI(t, h, "GET", "/readyz", "", 503, "")
	for _, p := range []string{"/leases", "/leases/192.168.10.100", "/subnets", "/reservations", "/relays", "/relays/10.0.0.1", "/vlans/10/leases"} {
		callAPI(t, h, "GET", "/api/v1"+p, "", 500, "test-admin")
	}
	callAPI(t, h, "DELETE", "/api/v1/leases/192.168.10.100", "", 500, "test-admin")
	callAPI(t, h, "POST", "/api/v1/leases/192.168.10.100/reserve", "", 500, "test-admin")
	callAPI(t, h, "DELETE", "/api/v1/reservations/1", "", 500, "test-admin")
}
