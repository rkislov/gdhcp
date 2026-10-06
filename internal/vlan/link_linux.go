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

//go:build linux

package vlan

import (
	"fmt"

	"github.com/vishvananda/netlink"
)

// Ensure creates or updates a VLAN sub-interface on parent without restarting
// the process. The interface name is parent.vlanID.
func Ensure(parent string, vlanID int) (string, error) {
	if vlanID < 1 || vlanID > 4094 {
		return "", fmt.Errorf("vlan: id %d out of range", vlanID)
	}
	base, err := netlink.LinkByName(parent)
	if err != nil {
		return "", fmt.Errorf("vlan: parent %s: %w", parent, err)
	}
	name := fmt.Sprintf("%s.%d", parent, vlanID)
	if existing, err := netlink.LinkByName(name); err == nil {
		if err := netlink.LinkSetUp(existing); err != nil {
			return "", err
		}
		return name, nil
	}
	link := &netlink.Vlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:        name,
			ParentIndex: base.Attrs().Index,
		},
		VlanId: vlanID,
	}
	if err := netlink.LinkAdd(link); err != nil {
		return "", fmt.Errorf("vlan: add %s: %w", name, err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return "", err
	}
	return name, nil
}

// Delete removes a VLAN sub-interface.
func Delete(name string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return err
	}
	return netlink.LinkDel(link)
}
