// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReloadWaitsForApplyAndReturnsItsError(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rejected"}[failure], func(t *testing.T) {
			path := filepath.Join(shortTempDir(t), "control.sock")
			listener, err := Listen(path)
			require.NoError(t, err)
			defer listener.Close()
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0600), info.Mode().Perm())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			served := make(chan error, 1)
			go func() {
				served <- ServeReload(ctx, listener, func() error {
					close(entered)
					<-release
					if failure {
						return errors.New("invalid YAML")
					}
					return nil
				})
			}()
			result := make(chan error, 1)
			go func() { result <- Reload(path, time.Second) }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("reload never reached process")
			}
			select {
			case <-result:
				t.Fatal("reload returned before application completed")
			case <-time.After(20 * time.Millisecond):
			}
			close(release)
			if failure {
				require.ErrorContains(t, <-result, "invalid YAML")
			} else {
				require.NoError(t, <-result)
			}
			cancel()
			require.NoError(t, <-served)
		})
	}
}

func TestNotifyUsesManagerSocket(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "notify.sock")
	socket, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	require.NoError(t, err)
	defer socket.Close()
	t.Setenv("NOTIFY_SOCKET", path)
	require.NoError(t, Notify("READY=1"))
	socket.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 128)
	n, _, err := socket.ReadFromUnix(buf)
	require.NoError(t, err)
	require.Equal(t, "READY=1", string(buf[:n]))
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gdr-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
