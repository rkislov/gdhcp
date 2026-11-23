// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActiveBackupManifest(t *testing.T) {
	source := `mode: active-backup
primary: a
failover_timeout: 40s
id: lab
node: a
listen: 192.0.2.11:8444
data_dir: /var/lib/godhcp/pair
ca: /etc/godhcp/ca.pem
cert: /etc/godhcp/a.pem
key: /etc/godhcp/a.key
peers:
 - {id: a, address: '192.0.2.11:8444', hostname: a.test}
 - {id: b, address: '192.0.2.12:8444', hostname: b.test}
fence: [/usr/libexec/godhcp/godhcp-fence-peer]
ip_up: [/usr/libexec/godhcp/godhcp-cluster-ip, up]
ip_down: [/usr/libexec/godhcp/godhcp-cluster-ip, down]
`
	path := filepath.Join(t.TempDir(), "cluster.yml")
	require.NoError(t, os.WriteFile(path, []byte(source), 0600))
	m, e := loadClusterManifest(path)
	require.NoError(t, e)
	require.Equal(t, 40*time.Second, m.FailoverTimeout)
	require.NoError(t, os.WriteFile(path, []byte(strings.Replace(source, "failover_timeout: 40s\n", "", 1)), 0600))
	defaults, e := loadClusterManifest(path)
	require.NoError(t, e)
	require.Equal(t, 30*time.Second, defaults.FailoverTimeout)

	legacy := strings.Replace(source, "mode: active-backup\nprimary: a\nfailover_timeout: 40s\n", "", 1)
	legacy = strings.Replace(legacy, "fence:", " - {id: c, address: '192.0.2.13:8444', hostname: c.test}\nfence:", 1)
	require.NoError(t, os.WriteFile(path, []byte(legacy), 0600))
	previous, e := loadClusterManifest(path)
	require.NoError(t, e)
	require.Equal(t, "raft", previous.Mode)

	for name, body := range map[string]string{
		"unknown":      strings.Replace(source, "active-backup", "two-node", 1),
		"no-primary":   strings.Replace(source, "primary: a", "primary: c", 1),
		"fencing-race": strings.Replace(source, "40s", "20s", 1),
		"too-fast":     strings.Replace(source, "40s", "1s", 1),
		"bootstrap":    source + "bootstrap: true\n",
		"raft-two":     strings.Replace(source, "active-backup", "raft", 1),
		"extra-peer":   strings.Replace(source, "fence:", " - {id: c, address: '192.0.2.13:8444', hostname: c.test}\nfence:", 1),
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, os.WriteFile(path, []byte(body), 0600))
			_, e := loadClusterManifest(path)
			require.Error(t, e)
		})
	}
}
