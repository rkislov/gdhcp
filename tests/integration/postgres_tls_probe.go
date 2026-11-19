//go:build ignore

// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0

// Built explicitly by scripts/test-postgres-tls.sh for a disposable database.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kislovrs/godhcp/internal/model"
	"github.com/kislovrs/godhcp/internal/storage"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	base := "host=db.example.net port=5432 user=godhcp dbname=godhcp connect_timeout=3 sslmode=verify-full sslrootcert=/tls/ca.pem sslcert=/tls/node.pem sslkey=/tls/node.key"
	db, err := sql.Open("pgx", base)
	if err != nil {
		return err
	}
	defer db.Close()
	var secure bool
	var version string
	if err := db.QueryRowContext(ctx, "SELECT ssl, version FROM pg_stat_ssl WHERE pid=pg_backend_pid()").Scan(&secure, &version); err != nil {
		return err
	}
	if !secure {
		return fmt.Errorf("connection is not TLS")
	}
	fmt.Println("PASS verified client/server TLS:", version)
	first, err := storage.Open(ctx, "postgres", base)
	if err != nil {
		return err
	}
	lease := model.Lease{IP: "192.0.2.101", MAC: "02:00:00:00:00:01", State: model.StateBound, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := first.UpsertLease(ctx, lease); err != nil {
		first.Close()
		return err
	}
	duid, err := first.(storage.IPv6Store).InitIPv6(ctx, nil)
	if err != nil {
		first.Close()
		return err
	}
	if err := first.Close(); err != nil {
		return err
	}
	second, err := storage.Open(ctx, "postgres", base)
	if err != nil {
		return err
	}
	defer second.Close()
	got, err := second.GetLease(ctx, lease.IP)
	if err != nil {
		return err
	}
	again, err := second.(storage.IPv6Store).InitIPv6(ctx, nil)
	if err != nil {
		return err
	}
	if got.MAC != lease.MAC || got.State != lease.State || string(duid) != string(again) {
		return fmt.Errorf("persisted state mismatch")
	}
	fmt.Println("PASS lease and DHCPv6 identity survive a new storage connection")
	cases := []struct{ name, dsn, reason string }{
		{"wrong CA", strings.Replace(base, "/tls/ca.pem", "/tls/other-ca.pem", 1), "unknown authority"},
		{"wrong hostname", strings.Replace(base, "host=db.example.net", "host=wrong.example.net", 1), "not wrong.example.net"},
		{"missing client certificate", strings.Replace(base, " sslcert=/tls/node.pem sslkey=/tls/node.key", "", 1), "requires a valid client certificate"},
		{"unmapped client identity", strings.Replace(strings.Replace(base, "/tls/node.pem", "/tls/rogue.pem", 1), "/tls/node.key", "/tls/rogue.key", 1), "certificate authentication failed"},
		{"plaintext", "host=db.example.net port=5432 user=godhcp dbname=godhcp connect_timeout=3 sslmode=disable", "pg_hba.conf rejects connection"},
	}
	for _, tc := range cases {
		conn, err := sql.Open("pgx", tc.dsn)
		if err == nil {
			err = conn.PingContext(ctx)
			conn.Close()
		}
		if err == nil || !strings.Contains(err.Error(), tc.reason) {
			return fmt.Errorf("%s: expected rejection %q, got %v", tc.name, tc.reason, err)
		}
		fmt.Println("PASS rejected", tc.name)
	}
	return nil
}
