// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0

package auth

func (p Principal) Global(min string) bool {
	for _, g := range p.Grants {
		if len(g.VLANIDs) == 0 && Allows(g.Role, min) {
			return true
		}
	}
	return false
}
func (p Principal) VLAN(id int, min string) bool {
	for _, g := range p.Grants {
		if !Allows(g.Role, min) {
			continue
		}
		if len(g.VLANIDs) == 0 {
			return true
		}
		for _, v := range g.VLANIDs {
			if v == id {
				return true
			}
		}
	}
	return false
}
