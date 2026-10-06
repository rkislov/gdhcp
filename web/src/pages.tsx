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

import Editor from "@monaco-editor/react";
import { FormEvent, useEffect, useState } from "react";
import { api, role, token, type Lease, type Page, type Relay, type Reservation, type Status, type Subnet, type User, type VLAN } from "./api";

export function Dashboard() {
  const [status, setStatus] = useState<Status | null>(null);
  const [vlans, setVlans] = useState<VLAN[]>([]);
  const [relays, setRelays] = useState<Relay[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    Promise.all([
      api<Status>("/api/v1/status"),
      api<Page<VLAN>>("/api/v1/vlans"),
      api<Page<Relay>>("/api/v1/relays"),
    ])
      .then(([s, v, r]) => {
        setStatus(s);
        setVlans(v.items ?? []);
        setRelays(r.items ?? []);
      })
      .catch((e: Error) => setError(e.message));
  }, []);

  if (error) return <p className="text-red-600">{error}</p>;
  if (!status) return <p>Загрузка…</p>;
  const hours = Math.floor(status.uptime_seconds / 3600);
  const mins = Math.floor((status.uptime_seconds % 3600) / 60);
  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-semibold">Обзор</h1>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="Активные аренды" value={String(status.leases_active)} />
        <Stat label="Подсети" value={String(status.subnets)} />
        <Stat label="VLAN" value={String(status.vlans)} />
        <Stat label="Аптайм" value={`${hours}ч ${mins}м`} />
      </div>
      <p className="text-sm text-zinc-500">
        {status.version} · {status.database} · {status.authoritative ? "authoritative" : "non-authoritative"} · {status.author}
      </p>
      <section className="card">
        <h2 className="mb-3 font-medium">Загрузка VLAN</h2>
        <div className="space-y-3">
          {vlans.map((v) => (
            <div key={v.id}>
              <div className="mb-1 flex justify-between text-sm">
                <span>
                  {v.id} {v.name} · {v.subnet_ref}
                </span>
                <span>
                  {v.pool_used ?? 0}/{v.pool_size ?? 0}
                </span>
              </div>
              <div className="h-2 rounded bg-zinc-200 dark:bg-zinc-800">
                <div
                  className="h-2 rounded bg-emerald-600"
                  style={{ width: `${Math.round((v.utilization ?? 0) * 100)}%` }}
                />
              </div>
            </div>
          ))}
        </div>
      </section>
      <section className="card">
        <h2 className="mb-2 font-medium">Relay</h2>
        {relays.length === 0 ? <p className="text-sm text-zinc-500">Доверенных relay пока нет в базе.</p> : null}
        <ul className="text-sm">
          {relays.map((r) => (
            <li key={r.giaddr}>
              {r.giaddr} · {r.remote_id || "—"} · {r.trusted ? "trusted" : "seen"}
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}

export function LeasesPage() {
  const [q, setQ] = useState("");
  const [vlan, setVlan] = useState("");
  const [items, setItems] = useState<Lease[]>([]);
  const [error, setError] = useState("");

  async function load(e?: FormEvent) {
    e?.preventDefault();
    const params = new URLSearchParams();
    if (q) params.set("q", q);
    if (vlan) params.set("vlan", vlan);
    try {
      const page = await api<Page<Lease>>("/api/v1/leases?" + params.toString());
      setItems(page.items ?? []);
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "ошибка");
    }
  }

  useEffect(() => {
    void load();
  }, []);

  function csv() {
    const header = "ip,mac,hostname,subnet,vlan,giaddr,state\n";
    const rows = items
      .map((l) => [l.ip, l.mac, l.hostname ?? "", l.subnet_id, l.vlan_id ?? "", l.giaddr ?? "", l.state].join(","))
      .join("\n");
    const blob = new Blob([header + rows], { type: "text/csv" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = "leases.csv";
    a.click();
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Аренды</h1>
        <button className="btn" onClick={csv}>
          CSV
        </button>
      </div>
      <form className="flex flex-wrap gap-2" onSubmit={load}>
        <input className="input max-w-xs" placeholder="поиск" value={q} onChange={(e) => setQ(e.target.value)} />
        <input className="input max-w-[8rem]" placeholder="VLAN" value={vlan} onChange={(e) => setVlan(e.target.value)} />
        <button className="btn-primary" type="submit">
          Найти
        </button>
      </form>
      {error && <p className="text-red-600">{error}</p>}
      <div className="card overflow-x-auto">
        <table className="table">
          <thead>
            <tr>
              <th>IP</th>
              <th>MAC</th>
              <th>Имя</th>
              <th>Подсеть</th>
              <th>VLAN</th>
              <th>Relay</th>
              <th>Состояние</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {items.map((l) => (
              <tr key={l.ip}>
                <td>{l.ip}</td>
                <td>{l.mac}</td>
                <td>{l.hostname}</td>
                <td>{l.subnet_id}</td>
                <td>{l.vlan_id}</td>
                <td>{l.giaddr}</td>
                <td>{l.state}</td>
                <td>
                  {role() !== "viewer" && (
                    <button
                      className="btn"
                      onClick={async () => {
                        await api("/api/v1/leases/" + l.ip, { method: "DELETE" });
                        void load();
                      }}
                    >
                      Снять
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {items.length === 0 && <p className="py-4 text-sm text-zinc-500">Аренд нет.</p>}
      </div>
    </div>
  );
}

export function SubnetsPage() {
  const [items, setItems] = useState<Subnet[]>([]);
  const [form, setForm] = useState({ id: "", network: "", start: "", end: "", gateway: "", vlan: "", domain: "" });
  const [error, setError] = useState("");

  async function load() {
    const page = await api<Page<Subnet>>("/api/v1/subnets");
    setItems(page.items ?? []);
  }
  useEffect(() => {
    void load().catch((e: Error) => setError(e.message));
  }, []);

  async function create(e: FormEvent) {
    e.preventDefault();
    setError("");
    try {
      await api("/api/v1/subnets", {
        method: "POST",
        body: JSON.stringify({
          id: form.id,
          network: form.network,
          range: [form.start, form.end],
          gateway: form.gateway,
          vlan: form.vlan ? Number(form.vlan) : undefined,
          domain: form.domain || undefined,
        }),
      });
      setForm({ id: "", network: "", start: "", end: "", gateway: "", vlan: "", domain: "" });
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "ошибка");
    }
  }

  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-semibold">Подсети</h1>
      {role() === "admin" && (
        <form className="card grid gap-2 md:grid-cols-3" onSubmit={create}>
          <input className="input" placeholder="id" value={form.id} onChange={(e) => setForm({ ...form, id: e.target.value })} required />
          <input className="input" placeholder="192.168.40.0/24" value={form.network} onChange={(e) => setForm({ ...form, network: e.target.value })} required />
          <input className="input" placeholder="gateway" value={form.gateway} onChange={(e) => setForm({ ...form, gateway: e.target.value })} />
          <input className="input" placeholder="range start" value={form.start} onChange={(e) => setForm({ ...form, start: e.target.value })} />
          <input className="input" placeholder="range end" value={form.end} onChange={(e) => setForm({ ...form, end: e.target.value })} />
          <input className="input" placeholder="vlan" value={form.vlan} onChange={(e) => setForm({ ...form, vlan: e.target.value })} />
          <button className="btn-primary" type="submit">
            Создать
          </button>
        </form>
      )}
      {error && <p className="text-red-600">{error}</p>}
      <div className="card overflow-x-auto">
        <table className="table">
          <thead>
            <tr>
              <th>ID</th>
              <th>Сеть</th>
              <th>Диапазон</th>
              <th>Шлюз</th>
              <th>VLAN</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {items.map((s) => (
              <tr key={s.id}>
                <td>{s.id}</td>
                <td>{s.network}</td>
                <td>
                  {s.range_start} – {s.range_end}
                </td>
                <td>{s.gateway}</td>
                <td>{s.vlan_id}</td>
                <td>
                  {role() === "admin" && (
                    <button
                      className="btn"
                      onClick={async () => {
                        await api("/api/v1/subnets/" + s.id, { method: "DELETE" });
                        await load();
                      }}
                    >
                      Удалить
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

export function ReservationsPage() {
  const [items, setItems] = useState<Reservation[]>([]);
  const [form, setForm] = useState({ mac: "", ip: "", hostname: "", subnet_id: "office-lan" });
  const [error, setError] = useState("");

  async function load() {
    const page = await api<Page<Reservation>>("/api/v1/reservations");
    setItems(page.items ?? []);
  }
  useEffect(() => {
    void load().catch((e: Error) => setError(e.message));
  }, []);

  async function create(e: FormEvent) {
    e.preventDefault();
    try {
      await api("/api/v1/reservations", { method: "POST", body: JSON.stringify(form) });
      setForm({ ...form, mac: "", ip: "", hostname: "" });
      await load();
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "ошибка");
    }
  }

  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-semibold">Резервации</h1>
      {role() !== "viewer" && (
        <form className="card grid gap-2 md:grid-cols-4" onSubmit={create}>
          <input className="input" placeholder="MAC" value={form.mac} onChange={(e) => setForm({ ...form, mac: e.target.value })} required />
          <input className="input" placeholder="IP" value={form.ip} onChange={(e) => setForm({ ...form, ip: e.target.value })} required />
          <input className="input" placeholder="hostname" value={form.hostname} onChange={(e) => setForm({ ...form, hostname: e.target.value })} />
          <input className="input" placeholder="subnet" value={form.subnet_id} onChange={(e) => setForm({ ...form, subnet_id: e.target.value })} required />
          <button className="btn-primary" type="submit">
            Добавить
          </button>
        </form>
      )}
      {error && <p className="text-red-600">{error}</p>}
      <div className="card overflow-x-auto">
        <table className="table">
          <thead>
            <tr>
              <th>MAC</th>
              <th>IP</th>
              <th>Имя</th>
              <th>Подсеть</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {items.map((r) => (
              <tr key={r.id}>
                <td>{r.mac}</td>
                <td>{r.ip}</td>
                <td>{r.hostname}</td>
                <td>{r.subnet_id}</td>
                <td>
                  {role() !== "viewer" && (
                    <button
                      className="btn"
                      onClick={async () => {
                        await api("/api/v1/reservations/" + r.id, { method: "DELETE" });
                        await load();
                      }}
                    >
                      Удалить
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

export function VlansPage() {
  const [items, setItems] = useState<VLAN[]>([]);
  const [form, setForm] = useState({ id: "", name: "", interface: "", priority: "0", subnet_ref: "", source: "local" });
  const [error, setError] = useState("");

  async function load() {
    const page = await api<Page<VLAN>>("/api/v1/vlans");
    setItems(page.items ?? []);
  }
  useEffect(() => {
    void load().catch((e: Error) => setError(e.message));
  }, []);

  async function create(e: FormEvent) {
    e.preventDefault();
    try {
      await api("/api/v1/vlans", {
        method: "POST",
        body: JSON.stringify({
          id: Number(form.id),
          name: form.name,
          interface: form.interface,
          priority: Number(form.priority),
          subnet_ref: form.subnet_ref,
          source: form.source,
        }),
      });
      await load();
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "ошибка");
    }
  }

  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-semibold">VLAN</h1>
      {role() === "admin" && (
        <form className="card grid gap-2 md:grid-cols-3" onSubmit={create}>
          <input className="input" placeholder="id" value={form.id} onChange={(e) => setForm({ ...form, id: e.target.value })} required />
          <input className="input" placeholder="имя" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          <input className="input" placeholder="eth0.40" value={form.interface} onChange={(e) => setForm({ ...form, interface: e.target.value })} />
          <input className="input" placeholder="pcp 0-7" value={form.priority} onChange={(e) => setForm({ ...form, priority: e.target.value })} />
          <input className="input" placeholder="subnet_ref" value={form.subnet_ref} onChange={(e) => setForm({ ...form, subnet_ref: e.target.value })} />
          <select className="input" value={form.source} onChange={(e) => setForm({ ...form, source: e.target.value })}>
            <option value="local">local</option>
            <option value="relay">relay</option>
          </select>
          <button className="btn-primary" type="submit">
            Создать
          </button>
        </form>
      )}
      {error && <p className="text-red-600">{error}</p>}
      <div className="card overflow-x-auto">
        <table className="table">
          <thead>
            <tr>
              <th>ID</th>
              <th>Имя</th>
              <th>Интерфейс</th>
              <th>PCP</th>
              <th>Подсеть</th>
              <th>Загрузка</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {items.map((v) => (
              <tr key={v.id}>
                <td>{v.id}</td>
                <td>{v.name}</td>
                <td>{v.interface}</td>
                <td>{v.priority}</td>
                <td>{v.subnet_ref}</td>
                <td className="min-w-32">
                  <div className="h-2 rounded bg-zinc-200 dark:bg-zinc-800">
                    <div className="h-2 rounded bg-sky-600" style={{ width: `${Math.round((v.utilization ?? 0) * 100)}%` }} />
                  </div>
                </td>
                <td>
                  {role() === "admin" && (
                    <button
                      className="btn"
                      onClick={async () => {
                        await api("/api/v1/vlans/" + v.id, { method: "DELETE" });
                        await load();
                      }}
                    >
                      Удалить
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

export function RelaysPage() {
  const [items, setItems] = useState<Relay[]>([]);
  const [giaddr, setGiaddr] = useState("");
  const [remote, setRemote] = useState("");
  const [circuit, setCircuit] = useState("Gi0/1:vlan20");
  const [parser, setParser] = useState("cisco");
  const [result, setResult] = useState("");
  const [error, setError] = useState("");

  async function load() {
    const page = await api<Page<Relay>>("/api/v1/relays");
    setItems(page.items ?? []);
  }
  useEffect(() => {
    void load().catch((e: Error) => setError(e.message));
  }, []);

  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-semibold">Relay</h1>
      {role() === "admin" && (
        <form
          className="card flex flex-wrap gap-2"
          onSubmit={async (e) => {
            e.preventDefault();
            try {
              await api("/api/v1/relays", { method: "POST", body: JSON.stringify({ giaddr, remote_id: remote }) });
              setGiaddr("");
              setRemote("");
              await load();
              setError("");
            } catch (err) {
              setError(err instanceof Error ? err.message : "ошибка");
            }
          }}
        >
          <input className="input max-w-xs" placeholder="giaddr" value={giaddr} onChange={(e) => setGiaddr(e.target.value)} required />
          <input className="input max-w-xs" placeholder="remote id" value={remote} onChange={(e) => setRemote(e.target.value)} />
          <button className="btn-primary" type="submit">
            Доверить
          </button>
        </form>
      )}
      <form
        className="card flex flex-wrap items-end gap-2"
        onSubmit={async (e) => {
          e.preventDefault();
          const out = await api<{ ok: boolean; vlan: number; parser: string }>("/api/v1/parsers/circuit-id/test", {
            method: "POST",
            body: JSON.stringify({ parser, circuit_id: circuit }),
          });
          setResult(out.ok ? `${out.parser}: VLAN ${out.vlan}` : "не распознан");
        }}
      >
        <label className="text-sm">
          Парсер
          <input className="input" value={parser} onChange={(e) => setParser(e.target.value)} />
        </label>
        <label className="text-sm">
          Circuit ID
          <input className="input min-w-64" value={circuit} onChange={(e) => setCircuit(e.target.value)} />
        </label>
        <button className="btn" type="submit">
          Проверить
        </button>
        {result && <span className="text-sm">{result}</span>}
      </form>
      {error && <p className="text-red-600">{error}</p>}
      <div className="card overflow-x-auto">
        <table className="table">
          <thead>
            <tr>
              <th>giaddr</th>
              <th>Remote ID</th>
              <th>Доверен</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {items.map((r) => (
              <tr key={r.giaddr}>
                <td>{r.giaddr}</td>
                <td>{r.remote_id}</td>
                <td>{r.trusted ? "да" : "нет"}</td>
                <td>
                  {role() === "admin" && r.trusted && (
                    <button
                      className="btn"
                      onClick={async () => {
                        await api("/api/v1/relays/" + encodeURIComponent(r.giaddr), { method: "DELETE" });
                        await load();
                      }}
                    >
                      Удалить
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

export function ConfigPage() {
  const [text, setText] = useState("");
  const [message, setMessage] = useState("");
  const dark = document.documentElement.classList.contains("dark");

  useEffect(() => {
    api<string>("/api/v1/config")
      .then(setText)
      .catch((e: Error) => setMessage(e.message));
  }, []);

  async function send(path: string, method: string) {
    setMessage("");
    try {
      await api(path, {
        method,
        headers: { "Content-Type": "application/yaml" },
        body: text,
      });
      setMessage(path.endsWith("validate") ? "Конфигурация корректна" : "Конфигурация применена");
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "ошибка");
    }
  }

  return (
    <div className="space-y-3">
      <h1 className="text-2xl font-semibold">Конфигурация</h1>
      <p className="text-sm text-zinc-500">Секреты в ответе заменены на ***. Перед сохранением верните настоящие значения.</p>
      <div className="card overflow-hidden p-0">
        <Editor height="480px" defaultLanguage="yaml" theme={dark ? "vs-dark" : "light"} value={text} onChange={(v) => setText(v ?? "")} />
      </div>
      <div className="flex gap-2">
        <button className="btn" onClick={() => send("/api/v1/config/validate", "POST")}>
          Проверить
        </button>
        {role() === "admin" && (
          <button className="btn-primary" onClick={() => send("/api/v1/config", "PUT")}>
            Сохранить
          </button>
        )}
      </div>
      {message && <p className="text-sm">{message}</p>}
    </div>
  );
}

export function LogsPage() {
  const [lines, setLines] = useState<string[]>([]);
  useEffect(() => {
    const q = token() ? "?access_token=" + encodeURIComponent(token()) : "";
    const es = new EventSource("/api/v1/logs/stream" + q);
    es.onmessage = (ev) => {
      setLines((prev) => [...prev.slice(-200), ev.data]);
    };
    return () => es.close();
  }, []);
  return (
    <div className="space-y-3">
      <h1 className="text-2xl font-semibold">Журнал</h1>
      <pre className="card max-h-[70vh] overflow-auto font-mono text-xs">{lines.join("\n") || "Ожидание записей…"}</pre>
    </div>
  );
}

export function UsersPage() {
  const [items, setItems] = useState<User[]>([]);
  const [form, setForm] = useState({ username: "", password: "", role: "viewer" });
  const [error, setError] = useState("");

  async function load() {
    const page = await api<Page<User>>("/api/v1/users");
    setItems(page.items ?? []);
  }
  useEffect(() => {
    void load().catch((e: Error) => setError(e.message));
  }, []);

  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-semibold">Пользователи</h1>
      {role() === "admin" && (
        <form
          className="card grid gap-2 md:grid-cols-4"
          onSubmit={async (e) => {
            e.preventDefault();
            try {
              await api("/api/v1/users", { method: "POST", body: JSON.stringify(form) });
              setForm({ username: "", password: "", role: "viewer" });
              await load();
              setError("");
            } catch (err) {
              setError(err instanceof Error ? err.message : "ошибка");
            }
          }}
        >
          <input className="input" placeholder="имя" value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} required />
          <input className="input" type="password" placeholder="пароль" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} required />
          <select className="input" value={form.role} onChange={(e) => setForm({ ...form, role: e.target.value })}>
            <option value="admin">admin</option>
            <option value="operator">operator</option>
            <option value="viewer">viewer</option>
          </select>
          <button className="btn-primary" type="submit">
            Создать
          </button>
        </form>
      )}
      {error && <p className="text-red-600">{error}</p>}
      <div className="card">
        <table className="table">
          <thead>
            <tr>
              <th>Имя</th>
              <th>Роль</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {items.map((u) => (
              <tr key={u.username}>
                <td>{u.username}</td>
                <td>{u.role}</td>
                <td>
                  {role() === "admin" && (
                    <button
                      className="btn"
                      onClick={async () => {
                        await api("/api/v1/users/" + encodeURIComponent(u.username), { method: "DELETE" });
                        await load();
                      }}
                    >
                      Удалить
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="card">
      <div className="text-sm text-zinc-500">{label}</div>
      <div className="text-2xl font-semibold">{value}</div>
    </div>
  );
}
