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

package vlan

import (
	"encoding/binary"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPCPAndVLAN(t *testing.T) {
	dst, _ := net.ParseMAC("ff:ff:ff:ff:ff:ff")
	src, _ := net.ParseMAC("02:00:00:00:00:01")
	frame, err := Build(dst, src, 20, 6, EtherIPv4, []byte{1, 2, 3, 4})
	require.NoError(t, err)
	require.Equal(t, TPID8021Q, binary.BigEndian.Uint16(frame[12:14]))
	require.Equal(t, TCI(6, 20), binary.BigEndian.Uint16(frame[14:16]))
	parsed, err := Parse(frame)
	require.NoError(t, err)
	require.Equal(t, 20, parsed.VLAN)
	require.Equal(t, 6, parsed.PCP)
	require.Equal(t, []byte{1, 2, 3, 4}, parsed.Payload)
	require.False(t, parsed.ServiceTag)
}

func TestQinQ(t *testing.T) {
	dst, _ := net.ParseMAC("ff:ff:ff:ff:ff:ff")
	src, _ := net.ParseMAC("02:00:00:00:00:01")
	frame, err := BuildQinQ(dst, src, 100, 5, 20, 6, EtherIPv4, []byte{9})
	require.NoError(t, err)
	require.Equal(t, TPID8021AD, binary.BigEndian.Uint16(frame[12:14]))
	parsed, err := Parse(frame)
	require.NoError(t, err)
	require.True(t, parsed.ServiceTag)
	require.Equal(t, 100, parsed.SVLAN)
	require.Equal(t, 5, parsed.SPCP)
	require.Equal(t, 20, parsed.VLAN)
	require.Equal(t, 6, parsed.PCP)
}

func TestInterfaceNames(t *testing.T) {
	v, ok := FromInterface("eth0.10")
	require.True(t, ok)
	require.Equal(t, 10, v)
	v, ok = FromInterface("vlan20")
	require.True(t, ok)
	require.Equal(t, 20, v)
	_, ok = FromInterface("eth0")
	require.False(t, ok)
}
