// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net"
	"path/filepath"
	"testing"

	"github.com/kislovrs/godhcp/internal/config"
	"github.com/stretchr/testify/require"
)

func TestStartupFailsWhenDHCPPortIsOccupied(t *testing.T) {
	socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
	require.NoError(t, err)
	defer socket.Close()
	cfg, err := config.Load("../../configs/dev.yml")
	require.NoError(t, err)
	cfg.Database.DSN = filepath.Join(t.TempDir(), "leases.db")
	cfg.Server.Listen = socket.LocalAddr().String()
	cfg.API.Listen = "127.0.0.1:0"
	cfg.Metrics.Enabled = false
	err = runDaemon(cfg, "")
	require.ErrorContains(t, err, "dhcp listen")
}
