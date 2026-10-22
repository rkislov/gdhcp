// Copyright 2026 Кислов Роман Сергеевич
// Licensed under the Apache License, Version 2.0.

package pool

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMovingClientDoesNotFreePreviousLease(t *testing.T) {
	m := New()
	spec := Spec{ID: "lan", Prefix: netip.MustParsePrefix("192.0.2.0/24"), Start: netip.MustParseAddr("192.0.2.10"), End: netip.MustParseAddr("192.0.2.20")}
	m.Reconcile([]Spec{spec}, nil)
	old, err := m.Allocate("lan", "client", "", spec.Start, true)
	require.NoError(t, err)
	_, err = m.Allocate("lan", "client", "", spec.Start.Next(), true)
	require.NoError(t, err)
	_, err = m.Allocate("lan", "other", "", old, true)
	require.ErrorIs(t, err, ErrConflict)
	m.Release(old.String(), "client")
	_, err = m.Allocate("lan", "other", "", old, true)
	require.NoError(t, err)
}
