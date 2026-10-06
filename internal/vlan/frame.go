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
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

const (
	TPID8021Q  uint16 = 0x8100
	TPID8021AD uint16 = 0x88a8
	EtherIPv4  uint16 = 0x0800
)

var ErrShort = errors.New("vlan: frame too short")

// Frame is an Ethernet header with an optional 802.1Q or QinQ tag.
type Frame struct {
	Dst        net.HardwareAddr
	Src        net.HardwareAddr
	ServiceTag bool
	SVLAN      int
	SPCP       int
	VLAN       int
	PCP        int
	EtherType  uint16
	Payload    []byte
}

// TCI encodes PCP and VLAN id into an 802.1Q tag control information field.
func TCI(pcp, vlanID int) uint16 {
	return uint16((pcp&0x7)<<13) | uint16(vlanID&0x0fff)
}

// Build encodes a single-tagged 802.1Q Ethernet frame.
func Build(dst, src net.HardwareAddr, vlanID, pcp int, etherType uint16, payload []byte) ([]byte, error) {
	return BuildFrame(Frame{
		Dst: dst, Src: src, VLAN: vlanID, PCP: pcp, EtherType: etherType, Payload: payload,
	})
}

// BuildQinQ encodes an 802.1ad service tag followed by an 802.1Q customer tag.
func BuildQinQ(dst, src net.HardwareAddr, sVLAN, sPCP, cVLAN, cPCP int, etherType uint16, payload []byte) ([]byte, error) {
	return BuildFrame(Frame{
		Dst: dst, Src: src, ServiceTag: true,
		SVLAN: sVLAN, SPCP: sPCP, VLAN: cVLAN, PCP: cPCP,
		EtherType: etherType, Payload: payload,
	})
}

// BuildFrame encodes f.
func BuildFrame(f Frame) ([]byte, error) {
	if len(f.Dst) < 6 || len(f.Src) < 6 {
		return nil, fmt.Errorf("vlan: hardware address must be 6 bytes")
	}
	if f.VLAN < 1 || f.VLAN > 4094 {
		return nil, fmt.Errorf("vlan: id %d out of range", f.VLAN)
	}
	n := 14 + len(f.Payload)
	if f.VLAN > 0 {
		n += 4
	}
	if f.ServiceTag {
		n += 4
	}
	buf := make([]byte, 0, n)
	buf = append(buf, f.Dst[:6]...)
	buf = append(buf, f.Src[:6]...)
	if f.ServiceTag {
		buf = appendUint16(buf, TPID8021AD)
		buf = appendUint16(buf, TCI(f.SPCP, f.SVLAN))
	}
	buf = appendUint16(buf, TPID8021Q)
	buf = appendUint16(buf, TCI(f.PCP, f.VLAN))
	et := f.EtherType
	if et == 0 {
		et = EtherIPv4
	}
	buf = appendUint16(buf, et)
	buf = append(buf, f.Payload...)
	return buf, nil
}

// Parse reads a tagged Ethernet frame. Untagged frames return VLAN 0.
func Parse(b []byte) (Frame, error) {
	if len(b) < 14 {
		return Frame{}, ErrShort
	}
	f := Frame{
		Dst: append(net.HardwareAddr(nil), b[0:6]...),
		Src: append(net.HardwareAddr(nil), b[6:12]...),
	}
	off := 12
	et := binary.BigEndian.Uint16(b[off : off+2])
	off += 2
	if et == TPID8021AD {
		if len(b) < off+4 {
			return Frame{}, ErrShort
		}
		f.ServiceTag = true
		sTCI := binary.BigEndian.Uint16(b[off : off+2])
		f.SPCP = int(sTCI >> 13)
		f.SVLAN = int(sTCI & 0x0fff)
		off += 2
		et = binary.BigEndian.Uint16(b[off : off+2])
		off += 2
	}
	if et == TPID8021Q {
		if len(b) < off+2 {
			return Frame{}, ErrShort
		}
		tci := binary.BigEndian.Uint16(b[off : off+2])
		f.PCP = int(tci >> 13)
		f.VLAN = int(tci & 0x0fff)
		off += 2
		if len(b) < off+2 {
			return Frame{}, ErrShort
		}
		et = binary.BigEndian.Uint16(b[off : off+2])
		off += 2
	}
	f.EtherType = et
	f.Payload = append([]byte(nil), b[off:]...)
	return f, nil
}

func appendUint16(b []byte, v uint16) []byte {
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], v)
	return append(b, buf[:]...)
}

// FromInterface extracts a VLAN id from names like eth0.10 or vlan10.
func FromInterface(name string) (int, bool) {
	name = strings.TrimSpace(name)
	if i := strings.LastIndex(name, "."); i >= 0 {
		if v, err := strconv.Atoi(name[i+1:]); err == nil && v >= 1 && v <= 4094 {
			return v, true
		}
	}
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "vlan") {
		if v, err := strconv.Atoi(lower[4:]); err == nil && v >= 1 && v <= 4094 {
			return v, true
		}
	}
	return 0, false
}
