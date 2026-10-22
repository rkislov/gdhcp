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

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kislovrs/godhcp/internal/api"
	"github.com/kislovrs/godhcp/internal/auth"
	"github.com/kislovrs/godhcp/internal/config"
	"github.com/kislovrs/godhcp/internal/core"
	"github.com/kislovrs/godhcp/internal/daemon"
	"github.com/kislovrs/godhcp/internal/dhcpdimport"
	"github.com/kislovrs/godhcp/internal/logbuf"
	"github.com/kislovrs/godhcp/internal/metrics"
	"github.com/kislovrs/godhcp/internal/server"
	"github.com/kislovrs/godhcp/internal/storage"
	"github.com/kislovrs/godhcp/internal/version"
	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "reload" {
		fs := flag.NewFlagSet("reload", flag.ExitOnError)
		socket := fs.String("socket", "/run/godhcp/control.sock", "running service control socket")
		timeout := fs.Duration("timeout", 20*time.Second, "reload acknowledgement timeout")
		_ = fs.Parse(os.Args[2:])
		if err := daemon.Reload(*socket, *timeout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("configuration reloaded")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "hash-password" {
		fs := flag.NewFlagSet("hash-password", flag.ExitOnError)
		password := fs.String("password", "", "password to hash")
		_ = fs.Parse(os.Args[2:])
		if *password == "" {
			fmt.Fprintln(os.Stderr, "usage: godhcp hash-password -password <secret>")
			os.Exit(2)
		}
		hash, err := auth.HashPassword(*password)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(hash)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("godhcp %s\nCopyright 2026 %s\nLicense: Apache-2.0\n", version.Version, version.Author)
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "import-dhcpd" || os.Args[1] == "import") {
		runImportDHCPD(os.Args[2:])
		return
	}

	fs := flag.NewFlagSet("godhcp", flag.ExitOnError)
	configPath := fs.String("config", env("GODHCP_CONFIG", "configs/dev.yml"), "path to config.yml")
	controlPath := fs.String("control-socket", "", "private Unix socket for synchronous reload")
	validateOnly := fs.Bool("validate", false, "validate the configuration and exit")
	_ = fs.Parse(os.Args[1:])

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *validateOnly {
		fmt.Println("configuration is valid")
		return
	}

	if err := runDaemon(cfg, *controlPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runDaemon(cfg *config.Config, controlPath string) error {
	logBuf := logbuf.New(1000)
	logger := newLogger(cfg, logBuf)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	store, err := storage.Open(ctx, cfg.Database.Driver, cfg.Database.DSN)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer store.Close()
	met := metrics.New()
	svc, err := core.New(ctx, core.Options{Store: store, Config: cfg, Logger: logger, Metrics: met})
	if err != nil {
		return fmt.Errorf("core: %w", err)
	}
	go svc.RunJanitor(ctx, 30*time.Second)
	// Every required listener is opened synchronously before READY=1.
	var udp *server.Listener
	if cfg.Server.Listen != "" && cfg.Server.Listen != "-" {
		udp, err = server.Listen(cfg.Server.Listen)
		if err != nil {
			return fmt.Errorf("dhcp listen: %w", err)
		}
		defer udp.Close()
	}
	httpSrv := &http.Server{Addr: cfg.API.Listen, Handler: api.New(svc, met, logBuf).Handler(), ReadHeaderTimeout: 10 * time.Second}
	httpListener, err := net.Listen("tcp", cfg.API.Listen)
	if err != nil {
		return fmt.Errorf("http listen: %w", err)
	}
	defer httpListener.Close()
	if cfg.API.TLS.Enabled {
		cert, err := tls.LoadX509KeyPair(cfg.API.TLS.Cert, cfg.API.TLS.Key)
		if err != nil {
			return fmt.Errorf("http tls: %w", err)
		}
		httpSrv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	}
	var metricsSrv *http.Server
	var metricsListener net.Listener
	if cfg.Metrics.Enabled && cfg.Metrics.Listen != "" && cfg.Metrics.Listen != cfg.API.Listen {
		metricsListener, err = net.Listen("tcp", cfg.Metrics.Listen)
		if err != nil {
			return fmt.Errorf("metrics listen: %w", err)
		}
		defer metricsListener.Close()
		metricsSrv = &http.Server{Addr: cfg.Metrics.Listen, Handler: met.Handler(), ReadHeaderTimeout: 5 * time.Second}
	}
	// cfg remains the immutable startup configuration. Listener settings require restart.
	var reloadMu sync.Mutex
	reload := func() error {
		reloadMu.Lock()
		defer reloadMu.Unlock()
		next, err := config.Load(cfg.Path)
		if err == nil {
			err = svc.Apply(ctx, next)
		}
		if err != nil {
			logger.Error("reload", "err", err)
			return err
		}
		logger.Info("configuration reloaded", "path", cfg.Path)
		return nil
	}
	failures := make(chan error, 4)
	supervise := func(name string, fn func() error) {
		go func() {
			if err := fn(); err != nil && err != http.ErrServerClosed && ctx.Err() == nil {
				failures <- fmt.Errorf("%s: %w", name, err)
			}
		}()
	}
	if controlPath != "" {
		control, err := daemon.Listen(controlPath)
		if err != nil {
			return fmt.Errorf("control listen: %w", err)
		}
		defer control.Close()
		supervise("control", func() error { return daemon.ServeReload(ctx, control, reload) })
	}
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go func() {
		for {
			select {
			case <-hup:
				_ = reload()
			case <-ctx.Done():
				return
			}
		}
	}()
	supervise("http", func() error {
		if cfg.API.TLS.Enabled {
			return httpSrv.ServeTLS(httpListener, "", "")
		}
		return httpSrv.Serve(httpListener)
	})
	if metricsSrv != nil {
		supervise("metrics", func() error { return metricsSrv.Serve(metricsListener) })
	}
	if udp != nil {
		supervise("dhcp", func() error { return udp.Serve(ctx, svc, logger) })
		logger.Info("dhcp listening", "addr", cfg.Server.Listen)
	}
	logger.Info("api listening", "addr", cfg.API.Listen)
	if err := daemon.Notify("READY=1"); err != nil {
		return fmt.Errorf("systemd ready: %w", err)
	}
	var failure error
	select {
	case <-ctx.Done():
	case failure = <-failures:
		logger.Error("service failed", "err", failure)
	}
	_ = daemon.Notify("STOPPING=1")
	stop()
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shut)
	if metricsSrv != nil {
		_ = metricsSrv.Shutdown(shut)
	}
	logger.Info("shutdown complete")
	return failure
}

func newLogger(cfg *config.Config, buf *logbuf.Buffer) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.Logging.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	var w io.Writer = os.Stdout
	if cfg.Logging.Output != "" && cfg.Logging.Output != "-" {
		f, err := os.OpenFile(cfg.Logging.Output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
		if err == nil {
			w = io.MultiWriter(os.Stdout, f)
		}
	}
	opts := &slog.HandlerOptions{Level: level}
	var base slog.Handler
	if cfg.Logging.Format == "text" {
		base = slog.NewTextHandler(w, opts)
	} else {
		base = slog.NewJSONHandler(w, opts)
	}
	return slog.New(logbuf.NewHandler(base, buf))
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func runImportDHCPD(args []string) {
	fs := flag.NewFlagSet("import-dhcpd", flag.ExitOnError)
	in := fs.String("in", "", "path to ISC dhcpd.conf (includes are resolved)")
	out := fs.String("out", "", "write GoDHCP YAML here (default: stdout)")
	_ = fs.Parse(args)
	if *in == "" {
		fmt.Fprintln(os.Stderr, "usage: godhcp import-dhcpd -in /etc/dhcp/dhcpd.conf [-out config.yml]")
		os.Exit(2)
	}
	cfg, doc, err := dhcpdimport.ImportFile(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, w := range doc.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	for _, f := range doc.Files {
		fmt.Fprintln(os.Stderr, "read:", f)
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	header := []byte("# Copyright 2026 Кислов Роман Сергеевич\n# Licensed under the Apache License, Version 2.0.\n# Imported from ISC dhcpd.conf by godhcp import-dhcpd\n")
	body := append(header, raw...)
	if *out == "" {
		_, _ = os.Stdout.Write(body)
		return
	}
	if err := os.WriteFile(*out, body, 0o640); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "wrote", *out)
}
