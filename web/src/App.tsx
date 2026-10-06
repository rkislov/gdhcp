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

import { FormEvent, useEffect, useState } from "react";
import { NavLink, Navigate, Route, Routes, useNavigate } from "react-router-dom";
import { api, clearSession, role, setSession, token, type Status } from "./api";
import {
  ConfigPage,
  Dashboard,
  LeasesPage,
  LogsPage,
  RelaysPage,
  ReservationsPage,
  SubnetsPage,
  UsersPage,
  VlansPage,
} from "./pages";

const links = [
  ["/", "Обзор"],
  ["/leases", "Аренды"],
  ["/subnets", "Подсети"],
  ["/reservations", "Резервации"],
  ["/vlans", "VLAN"],
  ["/relays", "Relay"],
  ["/config", "Конфиг"],
  ["/logs", "Журнал"],
  ["/users", "Пользователи"],
] as const;

export default function App() {
  const [ready, setReady] = useState(false);
  const [authed, setAuthed] = useState(Boolean(token()));

  useEffect(() => {
    api<Status>("/api/v1/status")
      .then(() => {
        if (!token()) setSession("", "admin");
        setAuthed(true);
      })
      .catch(() => setAuthed(Boolean(token())))
      .finally(() => setReady(true));
  }, []);

  if (!ready) {
    return <p className="p-8 text-zinc-500">Загрузка…</p>;
  }
  if (!authed) return <Login onDone={() => setAuthed(true)} />;

  return (
    <div className="min-h-screen md:grid md:grid-cols-[220px_1fr]">
      <aside className="border-b border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-900 md:border-b-0 md:border-r">
        <div className="mb-6">
          <div className="text-lg font-semibold">GoDHCP</div>
          <div className="text-xs text-zinc-500">{role() || "admin"}</div>
        </div>
        <nav className="flex gap-2 overflow-x-auto md:flex-col">
          {links.map(([to, label]) => (
            <NavLink
              key={to}
              to={to}
              end={to === "/"}
              className={({ isActive }) =>
                "rounded-md px-3 py-2 text-sm whitespace-nowrap " +
                (isActive
                  ? "bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900"
                  : "text-zinc-600 hover:bg-zinc-100 dark:text-zinc-300 dark:hover:bg-zinc-800")
              }
            >
              {label}
            </NavLink>
          ))}
        </nav>
      </aside>
      <div>
        <header className="flex items-center justify-end gap-3 border-b border-zinc-200 px-4 py-3 dark:border-zinc-800">
          <a className="text-sm text-zinc-500 underline" href="/swagger">
            Swagger
          </a>
          <button className="btn" onClick={toggleTheme}>
            Тема
          </button>
          <button
            className="btn"
            onClick={() => {
              clearSession();
              location.href = "/ui/";
            }}
          >
            Выйти
          </button>
        </header>
        <main className="p-4 md:p-6">
          <Routes>
            <Route path="/" element={<Dashboard />} />
            <Route path="/leases" element={<LeasesPage />} />
            <Route path="/subnets" element={<SubnetsPage />} />
            <Route path="/reservations" element={<ReservationsPage />} />
            <Route path="/vlans" element={<VlansPage />} />
            <Route path="/relays" element={<RelaysPage />} />
            <Route path="/config" element={<ConfigPage />} />
            <Route path="/logs" element={<LogsPage />} />
            <Route path="/users" element={<UsersPage />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </main>
      </div>
    </div>
  );
}

function Login({ onDone }: { onDone: () => void }) {
  const nav = useNavigate();
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError("");
    try {
      const tok = await api<{ access_token: string; role: string }>("/api/v1/auth/login", {
        method: "POST",
        body: JSON.stringify({ username, password }),
      });
      setSession(tok.access_token, tok.role);
      onDone();
      nav("/");
    } catch (err) {
      setError(err instanceof Error ? err.message : "ошибка входа");
    }
  }

  return (
    <div className="grid min-h-screen place-items-center p-4">
      <form onSubmit={submit} className="card w-full max-w-sm space-y-3">
        <h1 className="text-xl font-semibold">GoDHCP</h1>
        <p className="text-sm text-zinc-500">Вход в панель управления</p>
        <input className="input" value={username} onChange={(e) => setUsername(e.target.value)} placeholder="Имя" />
        <input className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="Пароль" />
        {error && <p className="text-sm text-red-600">{error}</p>}
        <button className="btn-primary w-full" type="submit">
          Войти
        </button>
      </form>
    </div>
  );
}

function toggleTheme() {
  const dark = document.documentElement.classList.toggle("dark");
  localStorage.setItem("godhcp.theme", dark ? "dark" : "light");
}
