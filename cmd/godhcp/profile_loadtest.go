// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0

//go:build loadtest

package main

import (
	"net"
	"net/http"
	"net/http/pprof"
	"os"
)

// Profiling is available only in the loadtest build inside an isolated lab.
func init() {
	if os.Getenv("GODHCP_LOAD_PROFILE") != "1" {
		return
	}
	listener, err := net.Listen("tcp", "127.0.0.1:6060")
	if err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	go func() { _ = http.Serve(listener, mux) }()
}
