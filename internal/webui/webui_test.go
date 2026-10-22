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

package webui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPagesAndStatic(t *testing.T) {
	h := Handler("/ui")
	res := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, res)
	require.Equal(t, http.StatusOK, rec.Code)
	body, _ := io.ReadAll(rec.Body)
	require.Contains(t, string(body), "GoDHCP")
	require.Contains(t, string(body), `data-page="dashboard"`)

	res = httptest.NewRequest(http.MethodGet, "/ui/static/style.css", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, res)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "--accent")

	res = httptest.NewRequest(http.MethodGet, "/ui/leases", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, res)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `data-page="leases"`)
}

func TestAllPagesAndCustomPrefix(t *testing.T) {
	for _, prefix := range []string{"/ui", "/network"} {
		for _, page := range []string{"", "leases", "subnets", "reservations", "vlans", "relays", "config", "logs", "users", "login"} {
			t.Run(prefix+"/"+page, func(t *testing.T) {
				rec := httptest.NewRecorder()
				Handler(prefix).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, prefix+"/"+page, nil))
				require.Equal(t, http.StatusOK, rec.Code)
				require.Contains(t, rec.Body.String(), `data-ui-prefix="`+prefix+`"`)
				require.Contains(t, rec.Body.String(), `src="`+prefix+`/static/ui-helpers.js"`)
				require.NotContains(t, rec.Body.String(), "GODHCP_UI_PREFIX=")
				require.NotContains(t, rec.Body.String(), "ZgotmplZ")
			})
		}
	}
}
