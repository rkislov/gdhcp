// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"syscall"
	"time"

	"github.com/hashicorp/raft"
	"github.com/kislovrs/godhcp/internal/cluster"
	"github.com/kislovrs/godhcp/internal/config"
	"github.com/kislovrs/godhcp/internal/daemon"
	"github.com/kislovrs/godhcp/internal/storage"
	"gopkg.in/yaml.v3"
)

type clusterManifest struct {
	Mode            string        `yaml:"mode"`
	Primary         string        `yaml:"primary"`
	FailoverTimeout time.Duration `yaml:"failover_timeout"`
	ID              string        `yaml:"id"`
	Node            string        `yaml:"node"`
	Listen          string        `yaml:"listen"`
	DataDir         string        `yaml:"data_dir"`
	Bootstrap       bool          `yaml:"bootstrap"`
	CA              string        `yaml:"ca"`
	Cert            string        `yaml:"cert"`
	Key             string        `yaml:"key"`
	Peers           []clusterPeer `yaml:"peers"`
	Fence           []string      `yaml:"fence"`
	IPUp            []string      `yaml:"ip_up"`
	IPDown          []string      `yaml:"ip_down"`
}
type clusterPeer struct {
	ID       string `yaml:"id"`
	Address  string `yaml:"address"`
	Hostname string `yaml:"hostname"`
}

func loadClusterManifest(path string) (clusterManifest, error) {
	var m clusterManifest
	f, e := os.Open(path)
	if e != nil {
		return m, e
	}
	defer f.Close()
	d := yaml.NewDecoder(io.LimitReader(f, 1<<20))
	d.KnownFields(true)
	if e = d.Decode(&m); e != nil {
		return m, e
	}
	if e = d.Decode(new(any)); e != io.EOF {
		return m, fmt.Errorf("cluster: exactly one YAML document required")
	}
	if m.Mode == "" {
		m.Mode = "raft"
	}
	expected := 3
	switch m.Mode {
	case "raft":
		if m.Primary != "" || m.FailoverTimeout != 0 {
			return m, fmt.Errorf("cluster: primary/failover_timeout require active-backup")
		}
	case "active-backup":
		expected = 2
		if m.Bootstrap {
			return m, fmt.Errorf("active-backup: bootstrap must be false; use primary")
		}
		if m.FailoverTimeout == 0 {
			m.FailoverTimeout = 30 * time.Second
		}
		if m.Primary == "" || m.FailoverTimeout < 30*time.Second || m.FailoverTimeout > 5*time.Minute {
			return m, fmt.Errorf("active-backup: primary and failover_timeout 30s..5m required")
		}
	default:
		return m, fmt.Errorf("cluster: unknown mode %q", m.Mode)
	}
	if m.ID == "" || m.Node == "" || m.Listen == "" || !filepath.IsAbs(m.DataDir) || len(m.Peers) != expected {
		return m, fmt.Errorf("cluster: id, node, listen, absolute data_dir and %d peers required", expected)
	}
	for _, p := range []string{m.CA, m.Cert, m.Key} {
		if !filepath.IsAbs(p) {
			return m, fmt.Errorf("cluster: absolute TLS paths required")
		}
	}
	for _, command := range [][]string{m.Fence, m.IPUp, m.IPDown} {
		if len(command) == 0 || !filepath.IsAbs(command[0]) {
			return m, fmt.Errorf("cluster: absolute fence, ip_up and ip_down commands required")
		}
	}
	validID := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)
	if !validID.MatchString(m.ID) || !validID.MatchString(m.Node) {
		return m, fmt.Errorf("cluster: invalid cluster or node ID")
	}
	seen := map[string]bool{}
	local := false
	for _, p := range m.Peers {
		if !validID.MatchString(p.ID) || seen[p.ID] || p.Hostname == "" {
			return m, fmt.Errorf("cluster: invalid peer")
		}
		seen[p.ID] = true
		if _, _, e := net.SplitHostPort(p.Address); e != nil {
			return m, e
		}
		if p.ID == m.Node {
			local = true
			if p.Address != m.Listen {
				return m, fmt.Errorf("cluster: local peer address must equal listen")
			}
		}
	}
	if m.Mode == "active-backup" && !seen[m.Primary] {
		return m, fmt.Errorf("active-backup: primary must be a peer")
	}
	if !local {
		return m, fmt.Errorf("cluster: local member missing")
	}
	return m, nil
}

func runCluster(base *config.Config, control string, m clusterManifest) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("cluster daemon requires Linux")
	}
	if base.HA.Enabled {
		return fmt.Errorf("cluster: disable legacy ha")
	}
	// Independent SQL writers must never share the materialized catalog.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	identities := []string{}
	peers := []cluster.Peer{}
	for _, p := range m.Peers {
		id := cluster.Identity(m.ID, p.ID)
		identities = append(identities, id)
		peers = append(peers, cluster.Peer{ID: p.ID, Address: p.Address, Hostname: p.Hostname, Identity: id})
	}
	security, e := cluster.TLS(m.CA, m.Cert, m.Key, cluster.Identity(m.ID, m.Node), identities)
	if e != nil {
		return e
	}
	listener, e := net.Listen("tcp", m.Listen)
	if e != nil {
		return e
	}
	defer listener.Close()
	if m.Mode == "active-backup" {
		return runPair(ctx, base, control, m, listener, security, peers)
	}
	state := cluster.NewLeaseState()
	node, e := cluster.Open(m.Node, m.DataDir, listener, security, peers, m.Bootstrap, state)
	if e != nil {
		return e
	}
	defer node.Close()
	hooks := &workloadHooks{base: base, control: control, manifest: m, node: node, state: state, parent: ctx}
	// A reboot must remove the node's own stale service IP before it is eligible.
	if e := hooks.ReleaseIP(ctx); e != nil {
		return e
	}
	if e := daemon.Notify("READY=1"); e != nil {
		return e
	}
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		if node.Raft.State() != raft.Leader {
			continue
		}
		takeover, cancel := context.WithTimeout(ctx, 90*time.Second)
		e = node.Takeover(takeover, m.Node, state, hooks)
		cancel()
		if e != nil {
			slog.Error("cluster activation blocked", "err", e)
			clean, c := context.WithTimeout(context.Background(), 20*time.Second)
			stopErr := hooks.Stop(clean)
			if stopErr == nil {
				stopErr = hooks.ReleaseIP(clean)
			}
			c()
			if stopErr != nil {
				return stopErr
			}
			continue
		}
		slog.Info("cluster active", "node", m.Node)
		active, cancel := context.WithCancel(ctx)
		maintained := make(chan error, 1)
		go func() { maintained <- node.Maintain(active, hooks) }()
		select {
		case <-hooks.exited:
			cancel()
			e = <-maintained
		case e = <-maintained:
			cancel()
		}
		// Even on an ordinary lost election, verify cleanup before allowing retries.
		clean, c := context.WithTimeout(context.Background(), 20*time.Second)
		stopErr := hooks.Stop(clean)
		if stopErr == nil {
			stopErr = hooks.ReleaseIP(clean)
		}
		c()
		if stopErr != nil {
			return stopErr
		}
		slog.Info("cluster inactive", "err", e)
	}
}

type workloadHooks struct {
	base     *config.Config
	control  string
	manifest clusterManifest
	node     *cluster.Node
	state    *cluster.LeaseState
	parent   context.Context
	cancel   context.CancelFunc
	exited   chan struct{}
	done     chan error
	catalog  storage.Store
}

func (h *workloadHooks) Stop(ctx context.Context) error {
	if h.cancel == nil {
		return nil
	}
	h.cancel()
	select {
	case <-h.exited:
	case <-ctx.Done():
		return ctx.Err()
	}
	err := <-h.done
	h.cancel = nil
	h.catalog.Close()
	h.catalog = nil
	if err != nil {
		slog.Warn("cluster workload stopped", "err", err)
	}
	return nil
}
func (h *workloadHooks) command(ctx context.Context, args []string, extra ...string) error {
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	argv := append(append([]string{}, args[1:]...), extra...)
	command := exec.CommandContext(bounded, args[0], argv...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		e := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(e, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return e
	}
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if e := command.Run(); e != nil {
		return fmt.Errorf("cluster hook %s failed: %w", args[0], e)
	}
	return nil
}
func (h *workloadHooks) Fence(ctx context.Context, id string) error {
	return h.command(ctx, h.manifest.Fence, id)
}
func (h *workloadHooks) AcquireIP(ctx context.Context) error { return h.command(ctx, h.manifest.IPUp) }
func (h *workloadHooks) ReleaseIP(ctx context.Context) error {
	return h.command(ctx, h.manifest.IPDown)
}
func (h *workloadHooks) Start(ctx context.Context) error {
	if h.cancel != nil {
		return fmt.Errorf("cluster workload already running")
	}
	catalog, e := storage.Open(ctx, "sqlite", ":memory:")
	if e != nil {
		return e
	}
	replicated := &cluster.LeaseStore{Store: catalog, Node: h.node, State: h.state}
	cfg, e := replicated.ResolveConfiguration(ctx, h.base)
	if e != nil {
		catalog.Close()
		return e
	}
	child, cancel := context.WithCancel(h.parent)
	h.cancel = cancel
	h.catalog = catalog
	h.done = make(chan error, 1)
	h.exited = make(chan struct{})
	ready := make(chan struct{})
	go func() {
		defer close(h.exited)
		h.done <- runService(child, cfg, h.control, replicated, func() { close(ready) })
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-h.exited:
		return fmt.Errorf("cluster workload exited before ready")
	case <-ready:
		return nil
	}
}

// runPair shares the existing DHCP/API/DDNS adapters, but not Raft election or
// activation. HTTP replication is only made available after stale VIP cleanup.
func runPair(ctx context.Context, base *config.Config, control string, m clusterManifest, listener net.Listener, security *tls.Config, peers []cluster.Peer) (result error) {
	state := cluster.NewLeaseState()
	hooks := &workloadHooks{base: base, control: control, manifest: m, state: state, parent: ctx}
	if err := hooks.ReleaseIP(ctx); err != nil {
		return err
	}
	node, err := cluster.OpenPair(m.Node, m.Primary, m.DataDir, listener, security, peers, state, m.FailoverTimeout, hooks.Fence)
	if err != nil {
		return err
	}
	defer node.Close()
	hooks.node = node
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cleanupErr := hooks.Stop(clean)
		if cleanupErr == nil {
			cleanupErr = hooks.ReleaseIP(clean)
		}
		if cleanupErr != nil {
			slog.Error("active-backup cleanup failed", "err", cleanupErr)
			result = errors.Join(result, cleanupErr)
		}
	}()
	if err = daemon.Notify("READY=1"); err != nil {
		return err
	}
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	started := false
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		step, cancel := context.WithTimeout(ctx, 25*time.Second)
		active, e := node.PairStep(step)
		cancel()
		if e != nil {
			return e
		}
		if active && !started {
			start, cancel := context.WithTimeout(ctx, 30*time.Second)
			e = hooks.AcquireIP(start)
			if e == nil {
				e = hooks.Start(start)
			}
			cancel()
			if e != nil {
				return e
			}
			started = true
			slog.Info("active-backup active", "node", m.Node)
		}
		if started {
			select {
			case <-hooks.exited:
				return fmt.Errorf("active-backup: workload exited")
			default:
			}
		}
	}
}
