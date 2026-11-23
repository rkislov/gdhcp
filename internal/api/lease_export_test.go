// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/kislovrs/godhcp/internal/config"
	"github.com/kislovrs/godhcp/internal/model"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestLeaseExportAllPagesSortAndAuthorization(t *testing.T) {
	s, h := workflowAPI(t)
	cfg, err := cloneConfig(s.svc.Config())
	require.NoError(t, err)
	cfg.API.Auth.APIKeys = append(cfg.API.Auth.APIKeys, config.APIKey{Name: "v10", Key: "v10", Role: "viewer", VLANIDs: []int{10}})
	require.NoError(t, s.commit(context.Background(), cfg))
	v := 10
	for i := 1; i <= 203; i++ {
		ip := fmt.Sprintf("10.0.116.%d", i)
		if i == 202 {
			ip = "10.0.117.2"
		}
		if i == 203 {
			ip = "10.0.118.2"
		}
		require.NoError(t, s.svc.Store().UpsertLease(context.Background(), model.Lease{IP: ip, MAC: fmt.Sprintf("02:00:00:00:00:%02x", i), SubnetID: "office-lan", VLANID: &v, State: "bound", ExpiresAt: time.Now().Add(time.Hour)}))
	}
	hidden := 20
	require.NoError(t, s.svc.Store().UpsertLease(context.Background(), model.Lease{IP: "10.0.119.1", MAC: "hidden", SubnetID: "voip-phones", VLANID: &hidden, State: "bound"}))
	read := func(query string) []model.Lease {
		rec := callAPI(t, h, "GET", "/api/v1/leases?"+query, "", 200, "v10")
		var out struct {
			Items []model.Lease `json:"items"`
			Total int           `json:"total"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		require.Equal(t, 203, out.Total)
		for _, l := range out.Items {
			require.NotEqual(t, "10.0.119.1", l.IP)
		}
		return out.Items
	}
	page := read("sort=ip&limit=200")
	require.Len(t, page, 200)
	require.Equal(t, "10.0.116.2", page[1].IP)
	page = read("sort=ip&limit=200&offset=200")
	require.Len(t, page, 3)
	require.Equal(t, "10.0.118.2", page[2].IP)
	all := read("sort=ip&order=desc&all=true&limit=1&offset=200")
	require.Len(t, all, 203)
	require.Equal(t, "10.0.118.2", all[0].IP)
	rec := callAPI(t, h, "GET", "/api/v1/leases?all=true&sort=ip&q=10.0.117.", "", 200, "v10")
	var filtered struct {
		Items []model.Lease `json:"items"`
		Total int           `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &filtered))
	require.Equal(t, 1, filtered.Total)
	require.Equal(t, "10.0.117.2", filtered.Items[0].IP)
	for _, query := range []string{"sort=unknown", "sort=ip&order=invalid"} {
		callAPI(t, h, "GET", "/api/v1/leases?"+query, "", 400, "v10")
	}
}
