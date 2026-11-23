// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0
package cluster

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
)

func FuzzPairImage(f *testing.F) {
	snapshot, _ := NewLeaseState().Snapshot()
	seed, _ := json.Marshal(pairImage{Version: 1, Data: json.RawMessage(snapshot.(leaseSnapshot))})
	f.Add(seed)
	f.Add([]byte(`{"Version":1,"Owner":"foreign","Data":{}}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		var v pairImage
		if json.Unmarshal(data, &v) != nil {
			return
		}
		p := &Pair{id: "a", peer: Peer{ID: "b"}}
		if p.validate(v) == nil {
			restored := NewLeaseState()
			if err := restored.Restore(io.NopCloser(bytes.NewReader(v.Data))); err != nil {
				t.Fatal(err)
			}
			if restored.index != v.Index {
				t.Fatal("accepted mismatched index")
			}
		}
		state := NewLeaseState()
		before, _ := state.Snapshot()
		if state.Restore(io.NopCloser(bytes.NewReader(v.Data))) != nil {
			after, _ := state.Snapshot()
			if !bytes.Equal(before.(leaseSnapshot), after.(leaseSnapshot)) {
				t.Fatal("rejected snapshot changed state")
			}
		}
	})
}
