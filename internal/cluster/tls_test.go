// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPeerMutualTLS(t *testing.T) {
	dir := t.TempDir()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caPath := filepath.Join(dir, "ca.pem")
	require.NoError(t, os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600))
	members := []string{Identity("lab", "a"), Identity("lab", "b"), Identity("lab", "c")}
	makeConfig := func(node, identity string, expired bool) *tls.Config {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		uri, err := url.Parse(identity)
		require.NoError(t, err)
		end := time.Now().Add(time.Hour)
		if expired {
			end = time.Now().Add(-time.Minute)
		}
		cert := &x509.Certificate{SerialNumber: big.NewInt(int64(len(node) + 2)), NotBefore: time.Now().Add(-time.Hour), NotAfter: end, DNSNames: []string{node + ".test"}, URIs: []*url.URL{uri}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		der, err := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, caKey)
		require.NoError(t, err)
		keyDER, err := x509.MarshalPKCS8PrivateKey(key)
		require.NoError(t, err)
		cp, kp := filepath.Join(dir, node+".pem"), filepath.Join(dir, node+".key")
		require.NoError(t, os.WriteFile(cp, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600))
		require.NoError(t, os.WriteFile(kp, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600))
		list := append(append([]string{}, members...), identity)
		if identity == members[0] || identity == members[1] || identity == members[2] {
			list = members
		}
		cfg, err := TLS(caPath, cp, kp, identity, list)
		require.NoError(t, err)
		return cfg
	}
	a := makeConfig("a", members[0], false)
	b := makeConfig("b", members[1], false)
	t.Run("durable consensus failover", func(t *testing.T) { testConsensus(t, []*tls.Config{a, b, makeConfig("c", members[2], false)}, members) })
	t.Run("lease failover", func(t *testing.T) {
		testLeaseFailover(t, []*tls.Config{a, b, makeConfig("c", members[2], false)}, members)
	})
	rogue := makeConfig("rogue", Identity("other", "c"), false)
	expired := makeConfig("expired", members[0], true)
	exchange := func(client *tls.Config) (error, error) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer listener.Close()
		result := make(chan error, 1)
		go func() {
			raw, err := listener.Accept()
			if err != nil {
				result <- err
				return
			}
			defer raw.Close()
			raw.SetDeadline(time.Now().Add(3 * time.Second))
			conn := tls.Server(raw, b)
			result <- conn.Handshake()
		}()
		raw, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		require.NoError(t, err)
		defer raw.Close()
		raw.SetDeadline(time.Now().Add(3 * time.Second))
		conn := tls.Client(raw, client)
		err = conn.Handshake()
		return err, <-result
	}
	t.Run("authorized", func(t *testing.T) {
		c, s := exchange(PeerTLS(a, "b.test", members[1]))
		require.NoError(t, c)
		require.NoError(t, s)
	})
	for name, cfg := range map[string]*tls.Config{
		"wrong hostname":  PeerTLS(a, "wrong.test", members[1]),
		"wrong member":    PeerTLS(a, "b.test", members[0]),
		"foreign cluster": PeerTLS(rogue, "b.test", members[1]),
		"expired client":  PeerTLS(expired, "b.test", members[1]),
	} {
		t.Run(name, func(t *testing.T) {
			c, s := exchange(cfg)
			require.True(t, c != nil || s != nil, "handshake must fail")
		})
	}
	t.Run("no client certificate", func(t *testing.T) {
		cfg := PeerTLS(a, "b.test", members[1])
		cfg.Certificates = nil
		c, s := exchange(cfg)
		require.True(t, c != nil || s != nil)
	})
	t.Run("TLS12 rejected", func(t *testing.T) {
		cfg := PeerTLS(a, "b.test", members[1])
		cfg.MinVersion = tls.VersionTLS12
		cfg.MaxVersion = tls.VersionTLS12
		c, s := exchange(cfg)
		require.True(t, c != nil || s != nil)
	})
}
