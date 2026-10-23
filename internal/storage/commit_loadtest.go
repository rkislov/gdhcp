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

package storage

import (
	"database/sql"
	"encoding/json"
	"os"
	"time"
)

var commitLog = func() *os.File {
	path := os.Getenv("GODHCP_COMMIT_LOG")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		panic(err)
	}
	return f
}()

func commitLeaseTx(tx *sql.Tx) error {
	if commitLog == nil {
		return tx.Commit()
	}
	start := time.Now()
	err := tx.Commit()
	elapsed := time.Since(start)
	if elapsed >= 5*time.Millisecond {
		json.NewEncoder(commitLog).Encode(map[string]any{"start_unix_ns": start.UnixNano(), "duration_ns": elapsed.Nanoseconds(), "error": err != nil})
	}
	return err
}
