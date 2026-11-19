// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
// GoDHCP cross-port definitions for BoltDB v1.3.1; upstream API/layout unchanged.
// Existing upstream 64-bit ports use the same limits. Conservatively copy
// unaligned key/value slices, as required by strict-alignment architectures.
//go:build loong64 || riscv64

package bolt

const maxMapSize = 0xFFFFFFFFFFFF
const maxAllocSize = 0x7FFFFFFF

var brokenUnaligned = true
