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
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kislovrs/godhcp/internal/api"
	"github.com/kislovrs/godhcp/internal/auth"
	"github.com/kislovrs/godhcp/internal/config"
	"github.com/kislovrs/godhcp/internal/core"
	"github.com/kislovrs/godhcp/internal/dhcpdimport"
	"github.com/kislovrs/godhcp/internal/logbuf"
	"github.com/kislovrs/godhcp/internal/metrics"
	"github.com/kislovrs/godhcp/internal/server"
	"github.com/kislovrs/godhcp/internal/storage"
	"github.com/kislovrs/godhcp/internal/version"
	"gopkg.in/yaml.v3"
)

func main() {
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
		fmt.Printf("godhcp %s\nCopyright 2026 %s\n", version.Version, version.Author)
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "import-dhcpd" || os.Args[1] == "import") {
		runImportDHCPD(os.Args[2:])
		return
	}

	fs := flag.NewFlagSet("godhcp", flag.ExitOnError)
	configPath := fs.String("config", env("GODHCP_CONFIG", "configs/dev.yml"), "path to config.yml")
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

	logBuf := logbuf.New(1000)
	logger := newLogger(cfg, logBuf)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := storage.Open(ctx, cfg.Database.Driver, cfg.Database.DSN)
	if err != nil {
		logger.Error("database", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	met := metrics.New()
	svc, err := core.New(ctx, core.Options{Store: store, Config: cfg, Logger: logger, Metrics: met})
	if err != nil {
		logger.Error("core", "err", err)
		os.Exit(1)
	}
	go svc.RunJanitor(ctx, 30*time.Second)

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		for range hup {
			next, err := config.Load(cfg.Path)
			if err != nil {
				logger.Error("reload", "err", err)
				continue
			}
			if err := svc.Apply(context.Background(), next); err != nil {
				logger.Error("reload", "err", err)
				continue
			}
			cfg = next
			logger.Info("configuration reloaded", "path", cfg.Path)
		}
	}()

	httpSrv := &http.Server{
		Addr:              cfg.API.Listen,
		Handler:           api.New(svc, met, logBuf).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		var err error
		if cfg.API.TLS.Enabled {
			err = httpSrv.ListenAndServeTLS(cfg.API.TLS.Cert, cfg.API.TLS.Key)
		} else {
			err = httpSrv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			logger.Error("http", "err", err)
			stop()
		}
	}()
	logger.Info("api listening", "addr", cfg.API.Listen)

	if cfg.Metrics.Enabled && cfg.Metrics.Listen != "" && cfg.Metrics.Listen != cfg.API.Listen {
		metricsSrv := &http.Server{Addr: cfg.Metrics.Listen, Handler: met.Handler(), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("metrics", "err", err)
			}
		}()
		go func() {
			<-ctx.Done()
			_ = metricsSrv.Close()
		}()
		logger.Info("metrics listening", "addr", cfg.Metrics.Listen)
	}

	if cfg.Server.Listen != "" && cfg.Server.Listen != "-" {
		if port, ok := privilegedUDPPort(cfg.Server.Listen); ok {
			logger.Warn("DHCP listens on a privileged UDP port; root or capabilities are required",
				"listen", cfg.Server.Listen,
				"port", port,
				"uid", os.Geteuid(),
				"hint", "Linux: CAP_NET_BIND_SERVICE, CAP_NET_RAW, CAP_NET_ADMIN (or run as root). Docker: --cap-add=NET_ADMIN --cap-add=NET_RAW --network host",
			)
		}
		go func() {
			if err := server.Serve(ctx, cfg.Server.Listen, svc, logger); err != nil {
				logger.Error("dhcp", "err", err)
			}
		}()
		logger.Info("dhcp listening", "addr", cfg.Server.Listen)
	}

	<-ctx.Done()
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shut)
	logger.Info("shutdown complete")
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

// privilegedUDPPort reports whether addr uses a privileged UDP port (< 1024).
func privilegedUDPPort(addr string) (int, bool) {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") {
			portStr = strings.TrimPrefix(addr, ":")
		} else {
			return 0, false
		}
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port >= 1024 {
		return port, false
	}
	return port, true
}
