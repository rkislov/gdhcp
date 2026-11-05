// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"github.com/kislovrs/godhcp/internal/auth"
	"github.com/kislovrs/godhcp/internal/config"
	"github.com/kislovrs/godhcp/internal/model"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVLANAccessIsEnforcedByAPI(t *testing.T) {
	s, h := workflowAPI(t)
	cfg, err := cloneConfig(s.svc.Config())
	require.NoError(t, err)
	cfg.API.Auth.APIKeys = append(cfg.API.Auth.APIKeys, config.APIKey{Name: "v10", Key: "v10", Role: "admin", VLANIDs: []int{10}}, config.APIKey{Name: "none", Key: "none", Role: "viewer", VLANIDs: []int{999}})
	require.NoError(t, s.commit(context.Background(), cfg))
	for _, tc := range []struct {
		ip, sub string
		v       int
	}{{"192.168.10.101", "office-lan", 10}, {"192.168.10.102", "office-lan", 10}, {"192.168.20.11", "voip-phones", 20}} {
		v := tc.v
		require.NoError(t, s.svc.Store().UpsertLease(context.Background(), model.Lease{IP: tc.ip, MAC: tc.ip, SubnetID: tc.sub, VLANID: &v, State: "bound", ExpiresAt: time.Now().Add(time.Hour)}))
	}
	for _, path := range []string{"/vlans/20", "/vlans/20/stats", "/vlans/20/leases", "/leases/192.168.20.11", "/config", "/users", "/access", "/logs/stream", "/metrics", "/relays"} {
		callAPI(t, h, "GET", "/api/v1"+path, "", 403, "v10")
	}
	for _, tc := range []struct{ m, p, b string }{
		{"DELETE", "/leases/192.168.20.11", ""},
		{"POST", "/leases/192.168.20.11/reserve", "{}"},
		{"DELETE", "/subnets/voip-phones", ""},
		{"PUT", "/subnets/voip-phones", `{"vlan":10}`},
		{"POST", "/reservations", `{"subnet_id":"voip-phones","ip":"192.168.20.12","mac":"02:00:00:00:00:12"}`},
		{"PUT", "/vlans/20", `{"subnet_ref":"office-lan"}`},
		{"POST", "/access/groups", `{"name":"escape","role":"admin"}`},
		{"PUT", "/access/ldap", `{}`},
		{"POST", "/config/reload", `{}`},
	} {
		callAPI(t, h, tc.m, "/api/v1"+tc.p, tc.b, 403, "v10")
	}
	for _, path := range []string{"/vlans", "/subnets", "/leases?limit=1&offset=1", "/reservations"} {
		rec := callAPI(t, h, "GET", "/api/v1"+path, "", 200, "v10")
		require.NotContains(t, rec.Body.String(), "voip")
		if strings.HasPrefix(path, "/leases") {
			var out struct {
				Total int           `json:"total"`
				Items []model.Lease `json:"items"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
			require.Equal(t, 2, out.Total)
			require.Len(t, out.Items, 1)
		}
	}
	rec := callAPI(t, h, "GET", "/api/v1/leases", "", 200, "none")
	require.JSONEq(t, `{"items":[],"total":0}`, rec.Body.String())
	callAPI(t, h, "POST", "/api/v1/reservations", `{"subnet_id":"office-lan","ip":"192.168.10.60","mac":"02:00:00:00:00:60"}`, 201, "v10")
	callAPI(t, h, "DELETE", "/api/v1/leases/192.168.10.101", "", 204, "v10")
	rec = callAPI(t, h, "GET", "/api/v1/vlans/10", "", 200, "test-admin")
	var vlan config.VLAN
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &vlan))
	vlan.Name = "Delegated name"
	raw, _ := json.Marshal(vlan)
	callAPI(t, h, "PUT", "/api/v1/vlans/10", string(raw), 200, "v10")
	vlan.Interface = "eth0.20"
	raw, _ = json.Marshal(vlan)
	callAPI(t, h, "PUT", "/api/v1/vlans/10", string(raw), 403, "v10")
	// A shared subnet cannot expose or alter data belonging to another VLAN.
	cfg, err = cloneConfig(s.svc.Config())
	require.NoError(t, err)
	cfg.VLANs = append(cfg.VLANs, config.VLAN{ID: 40, SubnetRef: "office-lan"})
	require.NoError(t, s.commit(context.Background(), cfg))
	callAPI(t, h, "GET", "/api/v1/leases/192.168.10.102", "", 403, "v10")
	rec = callAPI(t, h, "GET", "/api/v1/leases", "", 200, "v10")
	require.JSONEq(t, `{"items":[],"total":0}`, rec.Body.String())
}

func TestGroupsKeepRolesPerVLANAndTokensUseCurrentAssignments(t *testing.T) {
	s, h := workflowAPI(t)
	cfg, err := cloneConfig(s.svc.Config())
	require.NoError(t, err)
	cfg.API.Auth.Groups = []config.AccessGroup{{Name: "admins10", Role: "admin", VLANIDs: []int{10}}, {Name: "view20", Role: "viewer", VLANIDs: []int{20}}}
	cfg.API.Auth.Users = []config.User{{Username: "alice", Groups: []string{"admins10", "view20"}}}
	require.NoError(t, s.commit(context.Background(), cfg))
	tok, err := s.issuer().Issue("alice", "admin")
	require.NoError(t, err)
	request := func(method, path, body string, want int) {
		req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, want, rec.Code, rec.Body.String())
	}
	request("GET", "/vlans/20", "", 200)
	req := httptest.NewRequest("GET", "/api/v1/vlans", nil)
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var views struct {
		Items []struct {
			ID       int  `json:"id"`
			CanAdmin bool `json:"can_admin"`
		}
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &views))
	for _, v := range views.Items {
		require.Equal(t, v.ID == 10, v.CanAdmin)
	}

	request("PUT", "/vlans/20", `{"subnet_ref":"voip-phones"}`, 403)
	request("GET", "/config", "", 403)
	request("POST", "/reservations", `{"subnet_id":"office-lan","ip":"192.168.10.61","mac":"02:00:00:00:00:61"}`, 201)
	callAPI(t, h, "POST", "/api/v1/access/groups", `{"name":"admins10","role":"viewer","vlan_ids":[10]}`, 200, "test-admin")
	request("POST", "/reservations", `{"subnet_id":"office-lan"}`, 403)
	callAPI(t, h, "DELETE", "/api/v1/users/alice", "", 204, "test-admin")
	request("GET", "/status", "", 401)
	raw, _ := json.Marshal(map[string]string{"refresh_token": tok.RefreshToken})
	callAPI(t, h, "POST", "/api/v1/auth/refresh", string(raw), 401, "")
}

func TestLDAPSettingsRedactionAndGroupRevocation(t *testing.T) {
	s, h := workflowAPI(t)
	cfg, err := cloneConfig(s.svc.Config())
	require.NoError(t, err)
	hash, err := auth.HashPassword("local-secret")
	require.NoError(t, err)
	cfg.API.Auth.Users = []config.User{{Username: "local", Role: "admin", PasswordHash: hash}}
	require.NoError(t, s.commit(context.Background(), cfg))
	raw := `{"enabled":true,"url":"ldaps://ldap.example.org:636","bind_dn":"uid=svc,dc=example,dc=org","bind_password":"directory-secret","base_dn":"dc=example,dc=org","user_filter":"(uid={username})"}`
	callAPI(t, h, "PUT", "/api/v1/access/ldap", raw, 200, "test-admin")
	for _, path := range []string{"/access", "/config"} {
		rec := callAPI(t, h, "GET", "/api/v1"+path, "", 200, "test-admin")
		require.NotContains(t, rec.Body.String(), "directory-secret")
		require.Contains(t, rec.Body.String(), "***")
	}
	raw = strings.ReplaceAll(raw, "directory-secret", "***")
	callAPI(t, h, "PUT", "/api/v1/access/ldap", raw, 200, "test-admin")
	require.Equal(t, "directory-secret", s.svc.Config().API.Auth.LDAP.BindPassword)
	callAPI(t, h, "POST", "/api/v1/access/groups", `{"name":"ad-ops","role":"admin","vlan_ids":[10],"ldap_dn":"CN=Network,DC=example,DC=org"}`, 200, "test-admin")
	token, err := s.issuer().IssuePrincipal(auth.Principal{Name: "directory-user", Source: "ldap", Role: "admin", DirectoryGroups: []string{"cn=network,dc=EXAMPLE,dc=org"}})
	require.NoError(t, err)
	callAPI(t, h, "GET", "/api/v1/vlans/10?access_token="+token.AccessToken, "", 200, "")
	callAPI(t, h, "GET", "/api/v1/vlans/20?access_token="+token.AccessToken, "", 403, "")
	callAPI(t, h, "DELETE", "/api/v1/access/groups/ad-ops", "", 204, "test-admin")
	callAPI(t, h, "GET", "/api/v1/status?access_token="+token.AccessToken, "", 401, "")
}

func TestParserEditorUpsertPreviewAndRestore(t *testing.T) {
	_, h := workflowAPI(t)
	for _, pattern := range []string{`^port:(?P<vlan>\d+)$`, `^new:(?P<vlan>\d+)$`} {
		b, _ := json.Marshal(config.Parser{Name: "eltex", Regex: pattern, VLANGroup: "vlan"})
		callAPI(t, h, "POST", "/api/v1/parsers/circuit-id", string(b), 201, "test-admin")
	}
	rec := callAPI(t, h, "GET", "/api/v1/parsers/circuit-id", "", 200, "test-admin")
	var list struct{ Items, Custom []config.Parser }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Custom, 4) // three dev parsers plus Eltex override
	count := 0
	for _, p := range list.Items {
		if p.Name == "eltex" {
			count++
		}
	}
	require.Equal(t, 1, count)
	callAPI(t, h, "POST", "/api/v1/parsers/circuit-id/test", `{"regex":"^check:(?P<vlan>[0-9]+)$","circuit_id":"check:120"}`, 200, "test-admin")
	callAPI(t, h, "POST", "/api/v1/parsers/circuit-id", `{"name":"broken","regex":".*"}`, 400, "test-admin")
	callAPI(t, h, "DELETE", "/api/v1/parsers/circuit-id/eltex", "", 204, "test-admin")
	rec = callAPI(t, h, "POST", "/api/v1/parsers/circuit-id/test", `{"parser":"eltex","circuit_id":"eth 1/8:120"}`, 200, "test-admin")
	require.Contains(t, rec.Body.String(), `"ok":true`)
}

func TestFailedSaveRestoresActivePermissions(t *testing.T) {
	s, _ := workflowAPI(t)
	previous := s.svc.Config()
	cfg, err := cloneConfig(previous)
	require.NoError(t, err)
	cfg.Path = previous.Path + "/missing/config.yml"
	cfg.API.Auth.APIKeys[0].Role = "viewer"
	require.Error(t, s.commit(context.Background(), cfg))
	require.Equal(t, "admin", s.svc.Config().API.Auth.APIKeys[0].Role)
	require.Equal(t, previous.Path, s.svc.Config().Path)
}

func TestLocalLoginRemainsAvailableWithLDAPOffline(t *testing.T) {
	s := newAPIServer(t)
	cfg, err := cloneConfig(s.svc.Config())
	require.NoError(t, err)
	cfg.API.Auth.LDAP = config.LDAP{Enabled: true, URL: "ldaps://127.0.0.1:1", BindDN: "uid=svc,dc=example", BindPassword: "unused", BaseDN: "dc=example", UserFilter: "(uid={username})", TimeoutSeconds: 1}
	require.NoError(t, s.commit(context.Background(), cfg))
	rec := callAPI(t, s.Handler(), "POST", "/api/v1/auth/login", `{"username":"admin","password":"secret"}`, 200, "")
	var token auth.Tokens
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &token))
	p, err := s.issuer().Parse(token.AccessToken, "access")
	require.NoError(t, err)
	require.Empty(t, p.Source)
	_, valid := s.resolvePrincipal(auth.Principal{Name: "admin", Source: "ldap", DirectoryGroups: []string{"cn=admins,dc=example"}})
	require.False(t, valid)
	callAPI(t, s.Handler(), "POST", "/api/v1/auth/login", `{"username":"admin","password":"wrong"}`, 401, "")
}
