// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package cluster

import (
	"bytes"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"io"
	"testing"
)

type fakeActivation struct {
	Events  []string
	Failure string
}

func (h *fakeActivation) event(s string) error {
	h.Events = append(h.Events, s)
	if h.Failure == s {
		return errors.New("hook failure")
	}
	return nil
}
func (h *fakeActivation) Stop(context.Context) error               { return h.event("stop") }
func (h *fakeActivation) Fence(_ context.Context, id string) error { return h.event("fence:" + id) }
func (h *fakeActivation) AcquireIP(context.Context) error          { return h.event("ip-up") }
func (h *fakeActivation) ReleaseIP(context.Context) error          { return h.event("ip-down") }
func (h *fakeActivation) Start(context.Context) error              { return h.event("start") }
func TestUncertainOwnersSurviveSnapshot(t *testing.T) {
	s := NewLeaseState()
	require.Nil(t, s.applyActivation(ActivationCommand{Claim: "a"}))
	require.Nil(t, s.applyActivation(ActivationCommand{Expected: 1, Claim: "b"}))
	require.Nil(t, s.applyActivation(ActivationCommand{Expected: 2, Claim: "c"}))
	require.Equal(t, []string{"a", "b"}, s.Activation().Pending)
	snap, e := s.Snapshot()
	require.NoError(t, e)
	next := NewLeaseState()
	require.NoError(t, next.Restore(io.NopCloser(bytes.NewReader(snap.(leaseSnapshot)))))
	require.Equal(t, s.Activation(), next.Activation())
	require.NotNil(t, next.applyActivation(ActivationCommand{Expected: 1, Fenced: "a"}))
	require.Nil(t, next.applyActivation(ActivationCommand{Expected: 3, Fenced: "a"}))
	require.Equal(t, []string{"b"}, next.Activation().Pending)
}
func TestFailedStopDoesNotReleaseIP(t *testing.T) {
	h := &fakeActivation{Failure: "stop"}
	require.Error(t, cleanupActivation(h))
	require.Equal(t, []string{"stop"}, h.Events)
}
