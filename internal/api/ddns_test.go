// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package api

import (
	"github.com/kislovrs/godhcp/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDDNSSettingsValidationAndPersistence(t *testing.T) {
	s, h := workflowAPI(t)
	callAPI(t, h, "PUT", "/api/v1/ddns", `{"enabled":false,"server":"dc.example.org","zone":"hosts.example.org"}`, 200, "test-admin")
	require.Equal(t, "dc.example.org", s.svc.Config().DDNS.Server)
	saved, err := config.Load(s.svc.Config().Path)
	require.NoError(t, err)
	require.Equal(t, "dc.example.org", saved.DDNS.Server)
	rec := callAPI(t, h, "GET", "/api/v1/ddns", "", 200, "test-admin")
	require.Contains(t, rec.Body.String(), `"server":"dc.example.org"`)
	callAPI(t, h, "PUT", "/api/v1/ddns", `{"enabled":true,"server":"broken"}`, 400, "test-admin")
	require.False(t, s.svc.Config().DDNS.Enabled)
	require.Equal(t, "dc.example.org", s.svc.Config().DDNS.Server)
	callAPI(t, h, "GET", "/api/v1/ddns", "", 403, "test-view")
}
