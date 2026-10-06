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

package storage

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"time"
)

//go:embed schema_sqlite.sql
var sqliteSchema string

//go:embed schema_postgres.sql
var postgresSchema string

func (s *SQLStore) migrate(ctx context.Context) error {
	schema := sqliteSchema
	if s.dialect == "postgres" {
		schema = postgresSchema
	}
	for _, stmt := range statements(schema) {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("storage: migrate: %w", err)
		}
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		if _, err := s.db.ExecContext(ctx, s.q(`INSERT INTO schema_migrations(version, applied_at) VALUES (1, ?)`), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return nil
}

func statements(schema string) []string {
	var b strings.Builder
	for _, line := range strings.Split(schema, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	var out []string
	for _, p := range strings.Split(b.String(), ";") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
