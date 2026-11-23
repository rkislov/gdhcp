// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package cluster

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	boltdb "github.com/hashicorp/raft-boltdb/v2"
)

const pairLimit = 64 << 20

type pairImage struct {
	Version int
	Index   uint64
	Owner   string
	Data    json.RawMessage
}
type pairStatus struct {
	Role  string
	Image pairImage
}

// Pair is a fenced two-member primary/backup protocol, NOT a two-voter Raft.
// Eligibility is intentionally volatile: a restarted member must rejoin before
// it may take over. Fencing must prevent independent restart of the old process.
// Every acknowledged mutation is fsynced locally and on the backup, or locally
// after confirmed fencing. A lost response has an unknown outcome.
type Pair struct {
	mu          sync.Mutex
	id, primary string
	peer        Peer
	state       *LeaseState
	disk        *boltdb.BoltStore
	image       pairImage
	role        string
	armed, solo bool
	last        time.Time
	timeout     time.Duration
	fence       func(context.Context, string) error
	client      *http.Client
	server      *http.Server
	fatal       error
	closed      bool
}

func OpenPair(id, primary, dir string, listener net.Listener, security *tls.Config, peers []Peer, state *LeaseState, timeout time.Duration, fence func(context.Context, string) error) (*Node, error) {
	if len(peers) != 2 || state == nil || fence == nil || timeout < 30*time.Second || timeout > 5*time.Minute || security == nil || security.MinVersion < tls.VersionTLS13 || security.ClientAuth != tls.RequireAndVerifyClientCert || security.InsecureSkipVerify {
		return nil, fmt.Errorf("active-backup: two peers, verified mTLS, fencing and timeout 30s..5m required")
	}
	found, preferred := false, false
	var remote Peer
	for _, v := range peers {
		if v.ID == "" || v.Address == "" || v.Hostname == "" || v.Identity == "" {
			return nil, fmt.Errorf("active-backup: incomplete peer")
		}
		if v.ID == id {
			found = true
			if listener.Addr().String() != v.Address {
				return nil, fmt.Errorf("active-backup: local endpoint mismatch")
			}
		} else {
			remote = v
		}
		preferred = preferred || v.ID == primary
	}
	if !found || !preferred || peers[0].ID == peers[1].ID || peers[0].Address == peers[1].Address || peers[0].Identity == peers[1].Identity {
		return nil, fmt.Errorf("active-backup: invalid identities or primary")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	// Separate database format and topology: never open an existing Raft directory.
	if _, err := os.Stat(filepath.Join(dir, "raft.db")); !os.IsNotExist(err) {
		return nil, fmt.Errorf("active-backup: directory contains Raft state or cannot be inspected")
	}
	disk, err := boltdb.NewBoltStore(filepath.Join(dir, "pair.db"))
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			disk.Close()
		}
	}()
	identity, _ := json.Marshal(struct {
		ID, Primary string
		Peers       []Peer
	}{id, primary, peers})
	saved, err := disk.Get([]byte("identity"))
	if err != nil && !errors.Is(err, boltdb.ErrKeyNotFound) {
		return nil, err
	}
	if len(saved) == 0 {
		prior, readErr := disk.Get([]byte("image"))
		if len(prior) > 0 || (readErr != nil && !errors.Is(readErr, boltdb.ErrKeyNotFound)) {
			return nil, fmt.Errorf("active-backup: state lacks identity metadata")
		}
		if err = disk.Set([]byte("identity"), identity); err != nil {
			return nil, err
		}
	} else if !bytes.Equal(saved, identity) {
		return nil, fmt.Errorf("active-backup: directory identity/topology changed")
	}
	p := &Pair{id: id, primary: primary, peer: remote, state: state, disk: disk, role: "standby", timeout: timeout, fence: fence}
	b, err := disk.Get([]byte("image"))
	if err != nil && !errors.Is(err, boltdb.ErrKeyNotFound) {
		return nil, err
	}
	if len(b) > 0 {
		if err = json.Unmarshal(b, &p.image); err != nil {
			return nil, err
		}
		if err = p.validate(p.image); err != nil {
			return nil, err
		}
		if err = state.Restore(io.NopCloser(bytes.NewReader(p.image.Data))); err != nil {
			return nil, err
		}
	} else {
		snap, err := state.Snapshot()
		if err != nil {
			return nil, err
		}
		p.image = pairImage{Version: 1, Data: json.RawMessage(snap.(leaseSnapshot))}
	}
	p.client = &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: PeerTLS(security, remote.Hostname, remote.Identity), DialContext: (&net.Dialer{Timeout: 500 * time.Millisecond}).DialContext, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/state", p.serve)
	p.server = &http.Server{Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, MaxHeaderBytes: 8192}
	go func() {
		err := p.server.Serve(tls.NewListener(listener, security))
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			p.mu.Lock()
			p.fatal = err
			p.mu.Unlock()
		}
	}()
	ok = true
	return &Node{id: id, pair: p}, nil
}
func (p *Pair) decode(v pairImage) (*LeaseState, error) {
	if v.Version != 1 || len(v.Data) == 0 || len(v.Data) > pairLimit || (v.Owner != "" && v.Owner != p.id && v.Owner != p.peer.ID) {
		return nil, fmt.Errorf("active-backup: invalid image")
	}
	s := NewLeaseState()
	if err := s.Restore(io.NopCloser(bytes.NewReader(v.Data))); err != nil {
		return nil, err
	}
	if s.index != v.Index {
		return nil, fmt.Errorf("active-backup: image index mismatch")
	}
	return s, nil
}
func (p *Pair) validate(v pairImage) error {
	_, err := p.decode(v)
	return err
}
func (p *Pair) save(v pairImage) error {
	next, err := p.decode(v)
	if err != nil {
		return err
	}
	return p.savePrepared(v, next)
}

// next is private to this transaction and already validated. Transfer its maps
// only after fsync; decoding the entire snapshot again here doubles the backup's
// work within the replication deadline. Callers must not reuse next afterwards.
func (p *Pair) savePrepared(v pairImage, next *LeaseState) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > pairLimit {
		return fmt.Errorf("active-backup: snapshot exceeds limit")
	}
	if err = p.disk.Set([]byte("image"), b); err != nil {
		p.fatal = err
		return err
	}
	p.state.mu.Lock()
	p.state.leases, p.state.v6, p.state.duid = next.leases, next.v6, next.duid
	p.state.activation, p.state.configuration = next.activation, next.configuration
	p.state.dnsOwner, p.state.dns, p.state.index = next.dnsOwner, next.dns, next.index
	p.state.mu.Unlock()
	p.image = v
	return nil
}
func (p *Pair) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || !hasIdentity(r.TLS.PeerCertificates[0], p.peer.Identity) {
		http.Error(w, "unauthorized peer", 403)
		return
	}
	if p.closed || p.fatal != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		json.NewEncoder(w).Encode(pairStatus{p.role, p.image})
		return
	}
	if r.Method != http.MethodPut {
		w.WriteHeader(405)
		return
	}
	if p.role != "standby" {
		http.Error(w, "not standby", 409)
		return
	}
	var v pairImage
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, pairLimit))
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil || d.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid image", 400)
		return
	}
	// A freshly restarted, ineligible node may hold an unacknowledged local
	// write from its previous lifetime. The live owner replaces that image.
	// Once armed, rollback or conflicting replay is forbidden.
	if v.Owner != p.peer.ID || (p.armed && (v.Index < p.image.Index || (v.Index == p.image.Index && (!bytes.Equal(v.Data, p.image.Data) || v.Owner != p.image.Owner)))) {
		http.Error(w, "stale or conflicting image", 409)
		return
	}
	if v.Index != p.image.Index || v.Owner != p.image.Owner || !bytes.Equal(v.Data, p.image.Data) {
		next, err := p.decode(v)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := p.savePrepared(v, next); err != nil {
			http.Error(w, "persistence failed", 500)
			return
		}
	} else if v.Version != p.image.Version {
		http.Error(w, "invalid image version", 400)
		return
	}
	p.armed = true
	p.last = time.Now()
	w.WriteHeader(204)
}
func (p *Pair) request(ctx context.Context, method string, v *pairImage) (pairStatus, error) {
	var result pairStatus
	var body io.Reader
	if v != nil {
		b, e := json.Marshal(v)
		if e != nil {
			return result, e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, "https://"+p.peer.Address+"/v1/state", body)
	if e != nil {
		return result, e
	}
	resp, e := p.client.Do(req)
	if e != nil {
		return result, e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("active-backup: peer HTTP %d", resp.StatusCode)
	}
	if method == http.MethodGet {
		d := json.NewDecoder(io.LimitReader(resp.Body, pairLimit+1))
		d.DisallowUnknownFields()
		if e = d.Decode(&result); e != nil {
			return result, e
		}
		if d.Decode(new(any)) != io.EOF {
			return result, fmt.Errorf("active-backup: trailing status")
		}
		if e = p.validate(result.Image); e != nil {
			return result, e
		}
	}
	return result, nil
}

// mirror is called with the transaction lock. Failed replication never permits
// a local-only response until the peer has been positively fenced.
func (p *Pair) mirror(ctx context.Context) error {
	wasSolo := p.solo
	if p.solo {
		// GET cannot arm the receiver. Avoid repeatedly fencing an already powered-off
		// peer, but revoke local-only authority BEFORE any PUT that may arm it.
		status, err := p.request(ctx, http.MethodGet, nil)
		if err != nil {
			return ctx.Err()
		}
		if status.Role != "standby" {
			p.fatal = fmt.Errorf("active-backup: fenced peer unexpectedly active")
			return p.fatal
		}
		p.solo = false
	}
	_, replicationErr := p.request(ctx, http.MethodPut, &p.image)
	if replicationErr != nil && ctx.Err() == nil {
		// PUT is idempotent: a lost ACK or one slow exchange must not immediately
		// power off a healthy replica. Retry the SAME durable image once; never
		// acknowledge a new mutation or enter solo mode on an uncertain result.
		// Two HTTP deadlines plus the 20s fencing hook fit below the 30s takeover
		// minimum. The caller's shorter deadline still takes precedence.
		slog.Warn("active-backup retrying replication", "peer", p.peer.ID, "index", p.image.Index, "error", replicationErr)
		_, replicationErr = p.request(ctx, http.MethodPut, &p.image)
	}
	if replicationErr == nil {
		if wasSolo {
			slog.Info("active-backup replica synchronized", "peer", p.peer.ID, "index", p.image.Index)
		}
		return nil
	}
	slog.Warn("active-backup replication failed", "peer", p.peer.ID, "index", p.image.Index, "error", replicationErr)
	// This includes a lost acknowledgement after a returning peer accepted PUT.
	// Continuing solo without fencing again would allow stale backup promotion.
	if err := p.fence(ctx, p.peer.ID); err != nil {
		p.fatal = fmt.Errorf("active-backup: replication failed (%v); fencing failed: %w", replicationErr, err)
		return p.fatal
	}
	if err := ctx.Err(); err != nil {
		p.fatal = err
		return err
	}
	p.solo = true
	slog.Warn("active-backup degraded after confirmed fencing", "peer", p.peer.ID, "index", p.image.Index)
	return nil
}
func (p *Pair) authority(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.fatal != nil {
		return p.fatal
	}
	if p.closed || p.role != "active" {
		return fmt.Errorf("active-backup: not active")
	}
	return nil
}
func (p *Pair) commit(ctx context.Context, data []byte) (interface{}, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.closed || p.fatal != nil || p.role != "active" {
		return nil, fmt.Errorf("active-backup: no authority")
	}
	next := NewLeaseState()
	if err := next.Restore(io.NopCloser(bytes.NewReader(p.image.Data))); err != nil {
		return nil, err
	}
	result := next.Apply(&raft.Log{Index: p.image.Index + 1, Data: data})
	if err, ok := result.(error); ok {
		return nil, err
	}
	snap, err := next.Snapshot()
	if err != nil {
		return nil, err
	}
	if err = p.savePrepared(pairImage{1, p.image.Index + 1, p.id, json.RawMessage(snap.(leaseSnapshot))}, next); err != nil {
		return nil, err
	}
	if !p.solo {
		if err = p.mirror(ctx); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// PairStep drives replication and takeover. A restarted node is never armed by
// its disk alone. Initial/recovery coordination requires both members and the
// configured primary; the freshest durable image wins, without automatic failback.
func (n *Node) PairStep(ctx context.Context) (bool, error) {
	p := n.pair
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if p.fatal != nil {
		return false, p.fatal
	}
	if p.role == "active" {
		if p.solo {
			// An offline peer must not hold the transaction lock for its dial timeout.
			// GET cannot grant takeover eligibility; local writes remain safe until PUT.
			p.mu.Unlock()
			status, probeErr := p.request(ctx, http.MethodGet, nil)
			p.mu.Lock()
			if p.closed || p.fatal != nil {
				return false, fmt.Errorf("active-backup: unavailable during rejoin")
			}
			if probeErr != nil {
				return true, ctx.Err()
			}
			if status.Role != "standby" {
				p.fatal = fmt.Errorf("active-backup: fenced peer unexpectedly active")
				return false, p.fatal
			}
			p.solo = false
			err := p.mirror(ctx)
			if err == nil && !p.solo {
				slog.Info("active-backup replica synchronized", "peer", p.peer.ID, "index", p.image.Index)
			}
			return true, err
		}
		return true, p.mirror(ctx)
	}
	if p.armed {
		if time.Since(p.last) < p.timeout {
			return false, nil
		}
		p.role = "promoting"
		if err := p.fence(ctx, p.peer.ID); err != nil {
			p.fatal = fmt.Errorf("active-backup: takeover fencing failed: %w", err)
			return false, p.fatal
		}
		if err := ctx.Err(); err != nil {
			p.fatal = err
			return false, err
		}
		p.solo = true
	} else {
		if p.id != p.primary {
			return false, nil
		}
		// The other member may be rejoining us with PUT at the same time.
		// Never hold the receiver lock while probing its status.
		p.mu.Unlock()
		status, err := p.request(ctx, http.MethodGet, nil)
		p.mu.Lock()
		if p.closed || p.fatal != nil {
			return false, fmt.Errorf("active-backup: unavailable during startup")
		}
		if p.armed || p.role != "standby" {
			return false, nil
		}
		if err != nil {
			return false, ctx.Err()
		}
		if status.Role != "standby" {
			return false, nil
		}
		if status.Image.Index > p.image.Index {
			if err = p.save(status.Image); err != nil {
				return false, err
			}
		} else if status.Image.Index == p.image.Index && (!bytes.Equal(status.Image.Data, p.image.Data) || status.Image.Owner != p.image.Owner) {
			return false, fmt.Errorf("active-backup: divergent states")
		}
	}
	// Advancing the state index persists the ownership transition as well.
	next := NewLeaseState()
	if err := next.Restore(io.NopCloser(bytes.NewReader(p.image.Data))); err != nil {
		return false, err
	}
	next.index++
	snap, err := next.Snapshot()
	if err != nil {
		return false, err
	}
	proposal := pairImage{1, next.index, p.id, json.RawMessage(snap.(leaseSnapshot))}
	if !p.solo {
		// A failed startup proposal must not advance the local history ahead of
		// the backup indefinitely. The backup persists first; a lost ACK is safe.
		if _, err = p.request(ctx, http.MethodPut, &proposal); err != nil {
			return false, ctx.Err()
		}
	}
	if err = p.savePrepared(proposal, next); err != nil {
		return false, err
	}
	p.role = "active"
	return true, nil
}
func (p *Pair) close() {
	p.server.Close()
	p.client.CloseIdleConnections()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.disk.Close()
}
