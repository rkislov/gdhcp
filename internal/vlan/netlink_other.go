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

//go:build !linux

package vlan

import "fmt"

// LinkManager creates and deletes VLAN netdevices. On non-Linux systems the
// operations return an error; the API still records the VLAN in the database.
type LinkManager struct{}

// NewLinkManager returns a platform link manager.
func NewLinkManager() *LinkManager { return &LinkManager{} }

// EnsureVLAN reports that netlink is unavailable.
func (m *LinkManager) EnsureVLAN(parent string, id int) (string, error) {
	return "", fmt.Errorf("vlan: netlink is supported on linux only")
}

// DeleteVLAN reports that netlink is unavailable.
func (m *LinkManager) DeleteVLAN(name string) error {
	return fmt.Errorf("vlan: netlink is supported on linux only")
}
