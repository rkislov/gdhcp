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
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	go func() { _ = http.Serve(listener, mux) }()
}
