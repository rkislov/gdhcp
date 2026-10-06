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
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed templates/*.html static/*
var assets embed.FS

var pages = template.Must(template.ParseFS(assets, "templates/*.html"))

// Page is the data passed into every HTML template.
type Page struct {
	Title  string
	Active string
	Prefix string
}

// Handler serves multi-page HTML templates and static assets under Prefix.
func Handler(prefix string) http.Handler {
	prefix = strings.TrimRight(prefix, "/")
	if prefix == "" {
		prefix = "/ui"
	}
	static, err := fs.Sub(assets, "static")
	if err != nil {
		return http.NotFoundHandler()
	}
	files := http.StripPrefix(prefix+"/static/", http.FileServer(http.FS(static)))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, prefix)
		if path == "" {
			http.Redirect(w, r, prefix+"/", http.StatusFound)
			return
		}
		if strings.HasPrefix(path, "/static/") {
			files.ServeHTTP(w, r)
			return
		}
		name, title, active := route(path)
		if name == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := pages.ExecuteTemplate(w, name, Page{
			Title:  title,
			Active: active,
			Prefix: prefix,
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
}

func route(path string) (name, title, active string) {
	switch strings.Trim(path, "/") {
	case "", "dashboard":
		return "dashboard.html", "Обзор", "dashboard"
	case "leases":
		return "leases.html", "Аренды", "leases"
	case "subnets":
		return "subnets.html", "Подсети", "subnets"
	case "reservations":
		return "reservations.html", "Резервы", "reservations"
	case "vlans":
		return "vlans.html", "VLAN", "vlans"
	case "relays":
		return "relays.html", "Relay", "relays"
	case "config":
		return "config.html", "Конфиг", "config"
	case "logs":
		return "logs.html", "Журнал", "logs"
	case "users":
		return "users.html", "Пользователи", "users"
	case "login":
		return "login.html", "Вход", "login"
	default:
		return "", "", ""
	}
}
