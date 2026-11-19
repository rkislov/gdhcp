// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0

// Package cluster contains the authenticated transport for cluster peers.
// Transport authentication alone does not authorize DHCP service activation.
package cluster

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// Identity is an exact URI SAN, scoped to one cluster and member.
func Identity(cluster, node string) string {
	return "spiffe://godhcp/cluster/" + cluster + "/node/" + node
}

// TLS builds independent inbound and outbound configurations. The caller must
// replace these configurations and close old connections when membership changes.
// Members contains the authorized identity URIs, including the local identity.
func TLS(caFile, certFile, keyFile, local string, members []string) (*tls.Config, error) {
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("cluster: empty or invalid CA")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(members))
	for _, member := range members {
		if member == "" || allowed[member] {
			return nil, fmt.Errorf("cluster: empty or duplicate member")
		}
		allowed[member] = true
	}
	if !allowed[local] || !hasIdentity(leaf, local) {
		return nil, fmt.Errorf("cluster: local certificate identity mismatch")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert}, RootCAs: roots, ClientCAs: roots,
		ClientAuth: tls.RequireAndVerifyClientCert,
		// Reauthentication must not be bypassed through a resumed session.
		SessionTicketsDisabled: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
				return fmt.Errorf("cluster: unverified peer")
			}
			peer := state.PeerCertificates[0]
			if len(peer.URIs) != 1 {
				return fmt.Errorf("cluster: expected one peer identity")
			}
			id := peer.URIs[0].String()
			if id == local || !allowed[id] {
				return fmt.Errorf("cluster: unauthorized peer identity %q", id)
			}
			return nil
		},
	}, nil
}

// PeerTLS binds an outbound connection to both the expected DNS name and member.
func PeerTLS(base *tls.Config, hostname, identity string) *tls.Config {
	cfg := base.Clone()
	cfg.ServerName = hostname
	verify := cfg.VerifyConnection
	cfg.VerifyConnection = func(state tls.ConnectionState) error {
		if err := verify(state); err != nil {
			return err
		}
		if !hasIdentity(state.PeerCertificates[0], identity) {
			return fmt.Errorf("cluster: unexpected peer")
		}
		return nil
	}
	return cfg
}

func hasIdentity(cert *x509.Certificate, identity string) bool {
	return len(cert.URIs) == 1 && cert.URIs[0].String() == identity
}
