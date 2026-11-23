// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package cluster

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	boltdb "github.com/hashicorp/raft-boltdb/v2"
)

// Peer binds an advertised transport endpoint to an authenticated identity.
type Peer struct{ ID, Address, Hostname, Identity string }

// Node owns either a Raft log or a fenced two-member replication backend.
// Opening it does not activate DHCP or a VIP.
type Node struct {
	pair      *Pair
	id        string
	Raft      *raft.Raft
	transport *raft.NetworkTransport
	disk      *boltdb.BoltStore
	once      sync.Once
}

// Open starts a three-voter cluster member. Bootstrap is permitted on exactly
// one designated node, only for empty storage. FSM must implement deterministic
// application and snapshots of the entire replicated state.
func Open(id, dir string, listener net.Listener, security *tls.Config, peers []Peer, bootstrap bool, fsm raft.FSM) (*Node, error) {
	if len(peers) != 3 || fsm == nil || security == nil || security.MinVersion < tls.VersionTLS13 || security.ClientAuth != tls.RequireAndVerifyClientCert || security.InsecureSkipVerify {
		return nil, fmt.Errorf("cluster: three peers, FSM and verified mTLS required")
	}
	endpoints := map[raft.ServerAddress]Peer{}
	ids := map[string]bool{}
	configuration := raft.Configuration{}
	found := false
	for _, p := range peers {
		if p.ID == "" || p.Address == "" || p.Hostname == "" || p.Identity == "" || ids[p.ID] {
			return nil, fmt.Errorf("cluster: invalid peer")
		}
		if _, ok := endpoints[raft.ServerAddress(p.Address)]; ok {
			return nil, fmt.Errorf("cluster: duplicate endpoint")
		}
		ids[p.ID] = true
		endpoints[raft.ServerAddress(p.Address)] = p
		if p.ID == id {
			found = true
			if p.Address != listener.Addr().String() {
				return nil, fmt.Errorf("cluster: local endpoint mismatch")
			}
		}
		configuration.Servers = append(configuration.Servers, raft.Server{ID: raft.ServerID(p.ID), Address: raft.ServerAddress(p.Address), Suffrage: raft.Voter})
	}
	if !found {
		return nil, fmt.Errorf("cluster: unknown local member")
	}
	if _, err := os.Stat(filepath.Join(dir, "pair.db")); !os.IsNotExist(err) {
		return nil, fmt.Errorf("cluster: directory contains active-backup state or cannot be inspected")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	// Bind persistent storage to the exact cluster identities and topology.
	identity, err := json.Marshal(struct {
		ID    string
		Peers []Peer
	}{id, peers})
	if err != nil {
		return nil, err
	}
	identityPath := filepath.Join(dir, "identity.json")
	saved, readErr := os.ReadFile(identityPath)
	if os.IsNotExist(readErr) {
		if _, err := os.Stat(filepath.Join(dir, "raft.db")); err == nil {
			return nil, fmt.Errorf("cluster: existing log lacks identity metadata")
		}
		f, err := os.OpenFile(identityPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		_, err = f.Write(identity)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	} else if readErr != nil {
		return nil, readErr
	} else if !bytes.Equal(saved, identity) {
		return nil, fmt.Errorf("cluster: data directory identity or peers changed")
	}
	disk, err := boltdb.NewBoltStore(filepath.Join(dir, "raft.db"))
	if err != nil {
		return nil, err
	}
	snaps, err := raft.NewFileSnapshotStore(dir, 2, io.Discard)
	if err != nil {
		disk.Close()
		return nil, err
	}
	existing, err := raft.HasExistingState(disk, disk, snaps)
	if err != nil {
		disk.Close()
		return nil, err
	}
	transport := raft.NewNetworkTransport(&secureStream{Listener: listener, security: security, peers: endpoints}, 3, 2*time.Second, io.Discard)
	cfg := raft.DefaultConfig()
	cfg.LocalID = raft.ServerID(id)
	cfg.LogOutput = io.Discard
	if bootstrap && !existing {
		if err := raft.BootstrapCluster(cfg, disk, disk, snaps, transport, configuration); err != nil {
			transport.Close()
			disk.Close()
			return nil, err
		}
	}
	r, err := raft.NewRaft(cfg, fsm, disk, disk, snaps, transport)
	if err != nil {
		transport.Close()
		disk.Close()
		return nil, err
	}
	return &Node{id: id, Raft: r, transport: transport, disk: disk}, nil
}

func (n *Node) Close() {
	n.once.Do(func() {
		if n.pair != nil {
			n.pair.close()
			return
		}
		n.Raft.Shutdown().Error()
		n.transport.Close()
		n.disk.Close()
	})
}

// Commit returns success after durable replication and local application:
// majority persistence for Raft, or peer persistence/confirmed fencing for Pair.
// A timeout has an unknown outcome and must never authorize a DHCP response.
func (n *Node) Commit(data []byte, timeout time.Duration) error {
	if len(data) == 0 || len(data) > 1024*1024 || timeout <= 0 {
		return fmt.Errorf("cluster: invalid command")
	}
	if n.pair != nil {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		_, err := n.pair.commit(ctx, data)
		return err
	}
	f := n.Raft.Apply(append([]byte(nil), data...), timeout)
	done := make(chan error, 1)
	go func() {
		if err := f.Error(); err != nil {
			done <- err
			return
		}
		if err, ok := f.Response().(error); ok {
			done <- err
			return
		}
		done <- nil
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return fmt.Errorf("cluster: commit timeout, outcome unknown")
	}
}

type secureStream struct {
	net.Listener
	security *tls.Config
	peers    map[raft.ServerAddress]Peer
}

func (s *secureStream) Accept() (net.Conn, error) {
	for {
		raw, err := s.Listener.Accept()
		if err != nil {
			return nil, err
		}
		raw.SetDeadline(time.Now().Add(2 * time.Second))
		conn := tls.Server(raw, s.security)
		if err := conn.Handshake(); err != nil {
			raw.Close()
			continue
		}
		raw.SetDeadline(time.Time{})
		return conn, nil
	}
}
func (s *secureStream) Dial(address raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	peer, ok := s.peers[address]
	if !ok {
		return nil, fmt.Errorf("cluster: unknown endpoint")
	}
	return tls.DialWithDialer(&net.Dialer{Timeout: timeout}, "tcp", string(address), PeerTLS(s.security, peer.Hostname, peer.Identity))
}
