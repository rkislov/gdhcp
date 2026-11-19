// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/kislovrs/godhcp/internal/config"
)

// ConfigurationImage contains shared policy. Machine-specific paths, listener
// addresses and service credentials are excluded by sharedConfiguration.
type ConfigurationImage struct {
	Revision uint64
	Data     json.RawMessage `json:"data,omitempty"`
}
type ConfigurationCommand struct {
	Expected uint64
	Data     json.RawMessage `json:"data,omitempty"`
}

func cloneConfiguration(c *config.Config) (*config.Config, error) {
	b, e := json.Marshal(c)
	if e != nil {
		return nil, e
	}
	var next config.Config
	if e = json.Unmarshal(b, &next); e != nil {
		return nil, e
	}
	next.Path = c.Path
	return &next, nil
}
func sharedConfiguration(c *config.Config) ([]byte, error) {
	next, e := cloneConfiguration(c)
	if e != nil {
		return nil, e
	}
	next.ClusterRevision = 0
	next.Path = ""
	next.Database = config.Database{}
	next.Logging = config.Logging{}
	next.Metrics = config.Metrics{}
	next.API.Listen = ""
	next.API.TLS = config.TLS{}
	next.API.Auth.JWTSecret = ""
	next.API.Auth.LDAP.BindPassword = ""
	next.API.Auth.LDAP.CAFile = ""
	next.DDNS.KeyFile = ""
	next.DDNS.Keytab = ""
	next.Server.Listen = ""
	next.Server.Hostname = ""
	next.Server.ManageLinks = false
	next.Server.RawVLAN = false
	next.DHCPv6.Listen = ""
	return json.Marshal(next)
}

// MergeConfiguration overlays shared policy onto the receiving node's local
// transport and secret settings. Interface names must agree across members.
func MergeConfiguration(image ConfigurationImage, local *config.Config) (*config.Config, error) {
	if image.Revision == 0 || len(image.Data) == 0 {
		return nil, fmt.Errorf("cluster: configuration not initialized")
	}
	var next config.Config
	dec := json.NewDecoder(bytes.NewReader(image.Data))
	dec.DisallowUnknownFields()
	if e := dec.Decode(&next); e != nil {
		return nil, e
	}
	if dec.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("cluster: trailing configuration")
	}
	next.ClusterRevision = image.Revision
	next.Path = local.Path
	next.Database = local.Database
	next.Logging = local.Logging
	next.Metrics = local.Metrics
	next.API.Listen = local.API.Listen
	next.API.TLS = local.API.TLS
	next.API.Auth.JWTSecret = local.API.Auth.JWTSecret
	next.API.Auth.LDAP.BindPassword = local.API.Auth.LDAP.BindPassword
	next.API.Auth.LDAP.CAFile = local.API.Auth.LDAP.CAFile
	next.DDNS.KeyFile = local.DDNS.KeyFile
	next.DDNS.Keytab = local.DDNS.Keytab
	next.Server.Listen = local.Server.Listen
	next.Server.Hostname = local.Server.Hostname
	next.Server.ManageLinks = local.Server.ManageLinks
	next.Server.RawVLAN = local.Server.RawVLAN
	next.DHCPv6.Listen = local.DHCPv6.Listen
	next.ApplyDefaults()
	if e := next.Validate(); e != nil {
		return nil, e
	}
	if next.HA.Enabled {
		return nil, fmt.Errorf("cluster: legacy HA is incompatible")
	}
	return &next, nil
}
func validateConfigurationImage(image ConfigurationImage) error {
	if image.Revision == 0 {
		if len(image.Data) != 0 {
			return fmt.Errorf("cluster: invalid configuration revision")
		}
		return nil
	}
	if len(image.Data) == 0 || len(image.Data) > 512*1024 {
		return fmt.Errorf("cluster: invalid configuration size")
	}
	// Validate shared policy without reading local files or using machine secrets.
	local := &config.Config{}
	local.ApplyDefaults()
	local.API.Auth.JWTSecret = "cluster-validation-placeholder"
	local.API.Auth.LDAP.BindPassword = "placeholder"
	local.DDNS.Keytab = "/validation/keytab"
	local.DDNS.KeyFile = "/validation/key"
	merged, e := MergeConfiguration(image, local)
	if e != nil {
		return e
	}
	canonical, e := sharedConfiguration(merged)
	if e != nil {
		return e
	}
	// Reject hidden local secrets and noncanonical input rather than silently
	// retaining them in a replicated log or snapshot.
	if !bytes.Equal(canonical, image.Data) {
		return fmt.Errorf("cluster: configuration must be canonical shared policy")
	}
	return nil
}
func (s *LeaseState) applyConfiguration(c ConfigurationCommand) interface{} {
	if c.Expected != s.configuration.Revision {
		return fmt.Errorf("cluster: stale configuration revision")
	}
	image := ConfigurationImage{Revision: c.Expected + 1, Data: append([]byte(nil), c.Data...)}
	if image.Revision == 0 {
		return fmt.Errorf("cluster: configuration revision overflow")
	}
	if e := validateConfigurationImage(image); e != nil {
		return e
	}
	s.configuration = image
	return image.Revision
}
func (s *LeaseState) Configuration() ConfigurationImage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := s.configuration
	v.Data = append([]byte(nil), v.Data...)
	return v
}
func (s *LeaseStore) LoadConfiguration(ctx context.Context, local *config.Config) (*config.Config, uint64, error) {
	if e := s.CheckAuthority(ctx); e != nil {
		return nil, 0, e
	}
	image := s.State.Configuration()
	cfg, e := MergeConfiguration(image, local)
	return cfg, image.Revision, e
}
func (s *LeaseStore) CommitConfiguration(ctx context.Context, expected uint64, cfg *config.Config) (uint64, error) {
	if e := s.CheckAuthority(ctx); e != nil {
		return 0, e
	}
	next, e := cloneConfiguration(cfg)
	if e != nil {
		return 0, e
	}
	next.ApplyDefaults()
	if e := next.Validate(); e != nil {
		return 0, e
	}
	b, e := sharedConfiguration(next)
	if e != nil {
		return 0, e
	}
	result, e := s.Node.commitResult(ctx, LeaseCommand{Version: 1, Configuration: &ConfigurationCommand{Expected: expected, Data: b}})
	if e != nil {
		return 0, e
	}
	return result.(uint64), nil
}

// ResolveConfiguration loads committed policy, bootstrapping it only when the
// cluster has no policy yet. Restart never overwrites policy with stale YAML.
func (s *LeaseStore) ResolveConfiguration(ctx context.Context, local *config.Config) (*config.Config, error) {
	if e := s.CheckAuthority(ctx); e != nil {
		return nil, e
	}
	if s.State.Configuration().Revision == 0 {
		if _, e := s.CommitConfiguration(ctx, 0, local); e != nil {
			return nil, e
		}
	}
	result, _, e := s.LoadConfiguration(ctx, local)
	return result, e
}

// PersistConfiguration is invoked by core.Apply before publishing a snapshot.
func (s *LeaseStore) PersistConfiguration(ctx context.Context, previous, next *config.Config) error {
	if e := s.CheckAuthority(ctx); e != nil {
		return e
	}
	image := s.State.Configuration()
	if next.ClusterRevision != image.Revision {
		return fmt.Errorf("cluster: stale configuration revision; reload configuration")
	}
	b, e := sharedConfiguration(next)
	if e != nil {
		return e
	}
	if previous != nil {
		merged, e := MergeConfiguration(ConfigurationImage{Revision: image.Revision, Data: b}, previous)
		if e != nil {
			return e
		}
		proposed, e := cloneConfiguration(next)
		if e != nil {
			return e
		}
		proposed.ApplyDefaults()
		left, _ := json.Marshal(merged)
		right, _ := json.Marshal(proposed)
		if !bytes.Equal(left, right) {
			return fmt.Errorf("cluster: local listener or secret settings require local configuration and restart")
		}
	}
	if bytes.Equal(b, image.Data) {
		return nil
	}
	revision, e := s.CommitConfiguration(ctx, next.ClusterRevision, next)
	if e != nil {
		return e
	}
	next.ClusterRevision = revision
	return nil
}
