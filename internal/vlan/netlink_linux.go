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

// LinkManager creates 802.1Q netdevices with netlink.
type LinkManager struct{}

// NewLinkManager returns a Linux netlink manager.
func NewLinkManager() *LinkManager { return &LinkManager{} }

// EnsureVLAN creates parent.vlanID when it does not already exist.
func (m *LinkManager) EnsureVLAN(parent string, id int) (string, error) {
	if id < 1 || id > 4094 {
		return "", fmt.Errorf("vlan: id %d out of range", id)
	}
	name := fmt.Sprintf("%s.%d", parent, id)
	if _, err := netlink.LinkByName(name); err == nil {
		return name, nil
	}
	parentLink, err := netlink.LinkByName(parent)
	if err != nil {
		return "", err
	}
	link := &netlink.Vlan{VlanId: id, LinkAttrs: netlink.LinkAttrs{Name: name, ParentIndex: parentLink.Attrs().Index}}
	if err := netlink.LinkAdd(link); err != nil {
		return "", err
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return name, err
	}
	return name, nil
}

// DeleteVLAN removes a VLAN netdevice.
func (m *LinkManager) DeleteVLAN(name string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return err
	}
	return netlink.LinkDel(link)
}
