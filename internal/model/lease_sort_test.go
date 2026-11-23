// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package model

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestLeaseSortAllColumnsBeforePagination(t *testing.T) {
	v2, v10 := 2, 10
	a := Lease{IP: "10.0.116.2", Hostname: "alpha", MAC: "00:00:00:00:00:02", VLANID: &v2, SubnetID: "a", GIAddr: "10.0.116.2", State: "bound", ExpiresAt: time.Unix(10, 0)}
	b := Lease{IP: "10.0.116.10", Hostname: "Beta", MAC: "00:00:00:00:00:10", VLANID: &v10, SubnetID: "b", GIAddr: "10.0.116.10", State: "expired", ExpiresAt: time.Unix(10, 1)}
	for _, field := range []string{"ip", "hostname", "mac", "vlan_id", "subnet_id", "giaddr", "state", "expires_at"} {
		t.Run(field, func(t *testing.T) {
			require.True(t, ValidLeaseSort(field))
			for _, desc := range []bool{false, true} {
				page, total := LeasePage([]Lease{b, a}, LeaseFilter{Sort: field, Desc: desc, Limit: 1, Offset: 1})
				require.Equal(t, 2, total)
				want := b.IP
				if desc {
					want = a.IP
				}
				require.Equal(t, want, page[0].IP)
			}
		})
	}
	require.False(t, ValidLeaseSort("ip; DROP TABLE leases"))
	page, total := LeasePage([]Lease{b, a}, LeaseFilter{Sort: "ip", All: true, Limit: 1, Offset: 99})
	require.Equal(t, 2, total)
	require.Len(t, page, 2)
	require.Equal(t, a.IP, page[0].IP)
	page, _ = LeasePage([]Lease{b, a}, LeaseFilter{Sort: "ip", Offset: 99})
	require.Empty(t, page)
}

func TestLeaseSortIPv6AndDeterministicTies(t *testing.T) {
	for _, field := range []string{"ip", "hostname"} {
		page, _ := LeasePage([]Lease{{IP: "2001:db8::10"}, {IP: "2001:db8::2"}}, LeaseFilter{Sort: field, All: true})
		require.Equal(t, "2001:db8::2", page[0].IP)
	}
}
