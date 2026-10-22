// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0

// Package daemon implements the local service-manager control channel.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"
)

type response struct {
	Error string `json:"error,omitempty"`
}

// Listen opens a private Unix socket. Its parent must be owned by the service.
func Listen(path string) (*net.UnixListener, error) {
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

// ServeReload acknowledges only after the running process has applied the config.
func ServeReload(ctx context.Context, l *net.UnixListener, reload func() error) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			l.Close()
		case <-done:
		}
	}()
	for {
		c, err := l.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		c.SetDeadline(time.Now().Add(15 * time.Second))
		var command string
		var result response
		if err = json.NewDecoder(c).Decode(&command); err != nil {
			result.Error = err.Error()
		} else if command != "reload" {
			result.Error = "unsupported command"
		} else if err = reload(); err != nil {
			result.Error = err.Error()
		}
		_ = json.NewEncoder(c).Encode(result)
		c.Close()
	}
}

func Reload(path string, timeout time.Duration) error {
	c, err := net.DialTimeout("unix", path, timeout)
	if err != nil {
		return err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(timeout))
	if err = json.NewEncoder(c).Encode("reload"); err != nil {
		return err
	}
	var result response
	if err = json.NewDecoder(c).Decode(&result); err != nil {
		return err
	}
	if result.Error != "" {
		return fmt.Errorf("reload: %s", result.Error)
	}
	return nil
}

// Notify sends readiness to systemd when run from a Type=notify unit.
func Notify(state string) error {
	path := os.Getenv("NOTIFY_SOCKET")
	if path == "" {
		return nil
	}
	c, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.Write([]byte(state))
	return err
}
