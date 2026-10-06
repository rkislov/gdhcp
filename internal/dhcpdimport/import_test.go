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

package dhcpdimport

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExpandIncludesAndImport(t *testing.T) {
	dir := t.TempDir()
	office := filepath.Join(dir, "office.conf")
	require.NoError(t, os.WriteFile(office, []byte(`
subnet 192.168.10.0 netmask 255.255.255.0 {
  range 192.168.10.100 192.168.10.200;
  option routers 192.168.10.1;
  option domain-name-servers 1.1.1.1, 8.8.8.8;
  option domain-name "lan.local";
  host printer {
    hardware ethernet aa:bb:cc:dd:ee:ff;
    fixed-address 192.168.10.50;
  }
}
`), 0o644))

	nested := filepath.Join(dir, "nested.conf")
	require.NoError(t, os.WriteFile(nested, []byte(`
include "office.conf";
subnet 192.168.20.0 netmask 255.255.255.0 {
  range 192.168.20.10 192.168.20.50;
  option routers 192.168.20.1;
}
`), 0o644))

	main := filepath.Join(dir, "dhcpd.conf")
	require.NoError(t, os.WriteFile(main, []byte(`
# main
authoritative;
default-lease-time 3600;
max-lease-time 7200;
option domain-name-servers 9.9.9.9;
include "nested.conf";
host gateway-host {
  hardware ethernet 11:22:33:44:55:66;
  fixed-address 192.168.10.1;
}
`), 0o644))

	cfg, doc, err := ImportFile(main)
	require.NoError(t, err)
	require.True(t, cfg.Server.Authoritative)
	require.Equal(t, time.Hour, cfg.Server.LeaseDefault)
	require.Equal(t, 2*time.Hour, cfg.Server.LeaseMax)
	require.Len(t, cfg.Subnets, 2)
	require.Equal(t, "192.168.10.0/24", cfg.Subnets[0].Network)
	require.Equal(t, []string{"192.168.10.100", "192.168.10.200"}, cfg.Subnets[0].Range)
	require.Equal(t, "192.168.10.1", cfg.Subnets[0].Gateway)
	require.Equal(t, []string{"1.1.1.1", "8.8.8.8"}, cfg.Subnets[0].DNS)
	require.Len(t, cfg.Subnets[0].Reservations, 2) // printer + gateway-host matched by IP
	require.Len(t, doc.Files, 3)
	require.Contains(t, doc.Files[0], "dhcpd.conf")
}

func TestIncludeCycle(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.conf")
	b := filepath.Join(dir, "b.conf")
	require.NoError(t, os.WriteFile(a, []byte(`include "b.conf";`), 0o644))
	require.NoError(t, os.WriteFile(b, []byte(`include "a.conf";`), 0o644))
	_, _, err := Expand(a)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cycle")
}

func TestSharedNetworkAndGroup(t *testing.T) {
	src := `
shared-network office {
  option domain-name-servers 1.1.1.1;
  option routers 192.168.1.1;
  subnet 192.168.1.0 netmask 255.255.255.0 {
    range 192.168.1.50 192.168.1.60;
  }
  subnet 192.168.2.0 netmask 255.255.255.0 {
    range 192.168.2.50 192.168.2.60;
    option routers 192.168.2.1;
  }
}
group {
  next-server 10.0.0.2;
  filename "pxelinux.0";
  subnet 10.0.0.0 netmask 255.255.255.0 {
    range 10.0.0.10 10.0.0.20;
    option routers 10.0.0.1;
  }
}
`
	doc, err := Parse(src)
	require.NoError(t, err)
	require.Len(t, doc.Subnets, 3)
	require.Equal(t, "office", doc.Subnets[0].SharedNet)
	require.Equal(t, "192.168.1.1", doc.Subnets[0].Gateway)
	require.Equal(t, []string{"1.1.1.1"}, doc.Subnets[0].DNS)
	require.Equal(t, "192.168.2.1", doc.Subnets[1].Gateway)
	require.Equal(t, "10.0.0.2", doc.Subnets[2].NextServer)
	require.Equal(t, "pxelinux.0", doc.Subnets[2].BootFile)

	cfg, err := Convert(doc)
	require.NoError(t, err)
	require.Len(t, cfg.Subnets, 3)
}
