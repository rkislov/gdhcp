// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package model

import (
	"cmp"
	"net/netip"
	"slices"
	"strings"
)

// ValidLeaseSort restricts sorting to public lease fields.
func ValidLeaseSort(field string) bool {
	switch field {
	case "", "ip", "hostname", "mac", "vlan_id", "subnet_id", "giaddr", "state", "expires_at":
		return true
	}
	return false
}

func compareAddress(a, b string) int {
	x, ex := netip.ParseAddr(a)
	y, ey := netip.ParseAddr(b)
	if ex == nil && ey == nil {
		return x.Compare(y)
	}
	return strings.Compare(a, b)
}

// LeasePage applies a deterministic order before pagination in both SQL and cluster stores.
// The caller must apply authorization and search filters first.
func LeasePage(items []Lease, f LeaseFilter) ([]Lease, int) {
	slices.SortFunc(items, func(a, b Lease) int {
		var n int
		switch f.Sort {
		case "ip":
			n = compareAddress(a.IP, b.IP)
		case "hostname":
			n = strings.Compare(strings.ToLower(a.Hostname), strings.ToLower(b.Hostname))
		case "mac":
			n = strings.Compare(a.MAC, b.MAC)
		case "vlan_id":
			x, y := -1, -1
			if a.VLANID != nil {
				x = *a.VLANID
			}
			if b.VLANID != nil {
				y = *b.VLANID
			}
			n = cmp.Compare(x, y)
		case "subnet_id":
			n = strings.Compare(a.SubnetID, b.SubnetID)
		case "giaddr":
			n = compareAddress(a.GIAddr, b.GIAddr)
		case "state":
			n = strings.Compare(a.State, b.State)
		case "expires_at":
			n = a.ExpiresAt.Compare(b.ExpiresAt)
		default:
			n = strings.Compare(a.IP, b.IP)
		}
		if n == 0 {
			n = compareAddress(a.IP, b.IP)
		}
		if f.Desc {
			return -n
		}
		return n
	})
	total := len(items)
	if f.All {
		return items, total
	}
	offset := min(max(0, f.Offset), total)
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	return items[offset : offset+min(limit, total-offset)], total
}
