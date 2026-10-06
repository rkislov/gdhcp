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

package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPasswordAndJWT(t *testing.T) {
	hash, err := HashPassword("changeme")
	require.NoError(t, err)
	require.True(t, VerifyPassword(hash, "changeme"))
	require.False(t, VerifyPassword(hash, "nope"))

	mgr := New("secret-secret-secret-secret", time.Hour, 2*time.Hour, []User{{
		Username: "admin", PasswordHash: hash, Role: RoleAdmin,
	}}, []APIKey{{Name: "mon", Key: "k", Role: RoleViewer}})
	access, refresh, err := mgr.Login("admin", "changeme", time.Now())
	require.NoError(t, err)
	claims, err := mgr.Parse(access)
	require.NoError(t, err)
	require.Equal(t, RoleAdmin, claims.Role)
	require.Equal(t, "access", claims.Type)
	_, _, err = mgr.Refresh(refresh, time.Now())
	require.NoError(t, err)
	role, ok := mgr.RoleForAPIKey("k")
	require.True(t, ok)
	require.Equal(t, RoleViewer, role)
	require.True(t, AtLeast(RoleAdmin, RoleOperator))
	require.False(t, AtLeast(RoleViewer, RoleOperator))
}
