// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package cluster

import (
	"bytes"
	"github.com/kislovrs/godhcp/internal/config"
	"github.com/stretchr/testify/require"
	"io"
	"testing"
)

func TestConfigurationSnapshotAndLocalSecrets(t *testing.T) {
	cfg, e := config.Load("../../configs/example.yml")
	require.NoError(t, e)
	cfg.Database.DSN = "do-not-replicate-dsn"
	cfg.API.Auth.JWTSecret = "do-not-replicate-jwt-secret"
	cfg.API.Auth.LDAP.BindPassword = "do-not-replicate-ldap-password"
	cfg.DDNS.Keytab = "/private/source.keytab"
	data, e := sharedConfiguration(cfg)
	require.NoError(t, e)
	for _, secret := range []string{cfg.Database.DSN, cfg.API.Auth.JWTSecret, cfg.API.Auth.LDAP.BindPassword, cfg.DDNS.Keytab} {
		require.NotContains(t, string(data), secret)
	}
	state := NewLeaseState()
	result := state.applyConfiguration(ConfigurationCommand{Data: data})
	require.Equal(t, uint64(1), result)
	require.Error(t, state.applyConfiguration(ConfigurationCommand{Data: data}).(error))
	snap, e := state.Snapshot()
	require.NoError(t, e)
	next := NewLeaseState()
	require.NoError(t, next.Restore(io.NopCloser(bytes.NewReader(snap.(leaseSnapshot)))))
	local, e := config.Load("../../configs/example.yml")
	require.NoError(t, e)
	local.API.Auth.JWTSecret = "target-local-secret-must-survive"
	local.DDNS.Keytab = "/private/target.keytab"
	merged, e := MergeConfiguration(next.Configuration(), local)
	require.NoError(t, e)
	require.Equal(t, local.API.Auth.JWTSecret, merged.API.Auth.JWTSecret)
	require.Equal(t, local.DDNS.Keytab, merged.DDNS.Keytab)
	require.Equal(t, cfg.Subnets, merged.Subnets)
	image := next.Configuration()
	image.Data[0] = '!'
	require.NotEqual(t, image.Data, next.Configuration().Data)
}
