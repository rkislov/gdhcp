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

export type Page<T> = { items: T[]; total?: number };

export type Status = {
  version: string;
  author: string;
  authoritative: boolean;
  uptime_seconds: number;
  leases_active: number;
  subnets: number;
  vlans: number;
  database: string;
};

export type Lease = {
  ip: string;
  mac: string;
  hostname?: string;
  subnet_id: string;
  vlan_id?: number;
  giaddr?: string;
  circuit_id?: string;
  remote_id?: string;
  state: string;
  expires_at: string;
};

export type Subnet = {
  id: string;
  network: string;
  range_start?: string;
  range_end?: string;
  gateway?: string;
  vlan_id?: number;
  domain?: string;
  dns?: string[];
};

export type Reservation = {
  id: number;
  mac?: string;
  client_id?: string;
  ip: string;
  hostname?: string;
  subnet_id: string;
};

export type VLAN = {
  id: number;
  name?: string;
  interface?: string;
  priority?: number;
  source?: string;
  subnet_ref?: string;
  utilization?: number;
  pool_used?: number;
  pool_size?: number;
};

export type Relay = {
  giaddr: string;
  remote_id?: string;
  vendor?: string;
  trusted: boolean;
};

export type User = { username: string; role: string };

const TOKEN = "godhcp.token";
const ROLE = "godhcp.role";

export function token(): string {
  return localStorage.getItem(TOKEN) ?? "";
}

export function role(): string {
  return localStorage.getItem(ROLE) ?? "";
}

export function setSession(access: string, userRole: string) {
  localStorage.setItem(TOKEN, access);
  localStorage.setItem(ROLE, userRole);
}

export function clearSession() {
  localStorage.removeItem(TOKEN);
  localStorage.removeItem(ROLE);
}

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (token()) headers.set("Authorization", "Bearer " + token());
  if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  const res = await fetch(path, { ...init, headers });
  if (res.status === 401 && !path.includes("/auth/login")) {
    clearSession();
  }
  if (!res.ok) {
    let message = res.statusText;
    try {
      const body = await res.json();
      message = body?.error?.message || message;
    } catch {
      /* empty body */
    }
    throw new ApiError(res.status, message);
  }
  if (res.status === 204) return undefined as T;
  const type = res.headers.get("content-type") || "";
  if (type.includes("yaml") || type.includes("text/plain") || type.includes("text/event-stream")) {
    return (await res.text()) as T;
  }
  return (await res.json()) as T;
}
