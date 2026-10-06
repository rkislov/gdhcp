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
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
)

const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

var (
	ErrBadCredentials = errors.New("invalid credentials")
	ErrForbidden      = errors.New("forbidden")
	ErrUnauthorized   = errors.New("unauthorized")
)

// HashPassword returns a PHC-style argon2id hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=4$%s$%s",
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum)), nil
}

// VerifyPassword checks a PHC-style argon2id hash.
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var memory uint32
	var timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// Principal is an authenticated API caller.
type Principal struct {
	Name string
	Role string
}

// Rank orders roles. A higher rank includes the lower ones.
func Rank(role string) int {
	switch role {
	case RoleAdmin:
		return 3
	case RoleOperator:
		return 2
	case RoleViewer:
		return 1
	default:
		return 0
	}
}

// Allows reports whether role is at least min.
func Allows(role, min string) bool {
	return Rank(role) >= Rank(min)
}

type claims struct {
	Role string `json:"role"`
	Typ  string `json:"typ"`
	jwt.RegisteredClaims
}

// Tokens are signed access and refresh JWTs.
type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	TokenType    string    `json:"token_type"`
	Role         string    `json:"role"`
}

// Issuer signs and checks JWTs.
type Issuer struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	now        func() time.Time
}

func NewIssuer(secret string, accessTTL, refreshTTL time.Duration) *Issuer {
	if accessTTL <= 0 {
		accessTTL = time.Hour
	}
	if refreshTTL <= 0 {
		refreshTTL = 24 * time.Hour
	}
	return &Issuer{secret: []byte(secret), accessTTL: accessTTL, refreshTTL: refreshTTL, now: time.Now}
}

func (i *Issuer) Issue(name, role string) (Tokens, error) {
	now := i.now()
	accessExp := now.Add(i.accessTTL)
	access, err := i.sign(name, role, "access", accessExp)
	if err != nil {
		return Tokens{}, err
	}
	refresh, err := i.sign(name, role, "refresh", now.Add(i.refreshTTL))
	if err != nil {
		return Tokens{}, err
	}
	return Tokens{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    accessExp,
		TokenType:    "Bearer",
		Role:         role,
	}, nil
}

func (i *Issuer) sign(name, role, typ string, exp time.Time) (string, error) {
	c := claims{
		Role: role,
		Typ:  typ,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   name,
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(i.now()),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return t.SignedString(i.secret)
}

// Parse validates an access or refresh token.
func (i *Issuer) Parse(token, typ string) (Principal, error) {
	parsed, err := jwt.ParseWithClaims(token, &claims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return i.secret, nil
	})
	if err != nil || !parsed.Valid {
		return Principal{}, ErrUnauthorized
	}
	c, ok := parsed.Claims.(*claims)
	if !ok || c.Typ != typ || c.Subject == "" {
		return Principal{}, ErrUnauthorized
	}
	return Principal{Name: c.Subject, Role: c.Role}, nil
}
