/* Copyright 2026 Кислов Роман Сергеевич
   Licensed under the Apache License, Version 2.0. */

(function () {
  const prefix = window.GODHCP_UI_PREFIX || "/ui";
  const TOKEN_KEY = "godhcp_token";
  let token = localStorage.getItem(TOKEN_KEY) || "";

  const $ = (sel, root) => (root || document).querySelector(sel);
  const errBox = () => $("#err");

  function headers(extra) {
    const h = Object.assign({ "Content-Type": "application/json" }, extra || {});
    if (token) h.Authorization = "Bearer " + token;
    return h;
  }

  async function api(path, opt) {
    const res = await fetch("/api/v1" + path, Object.assign({}, opt, {
      headers: headers(opt && opt.headers),
    }));
    if (res.status === 401) {
      token = "";
      localStorage.removeItem(TOKEN_KEY);
      location.href = prefix + "/login";
      throw new Error("unauthorized");
    }
    if (res.status === 204) return null;
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error((body.error && body.error.message) || res.statusText);
    return body;
  }

  function showError(e) {
    const el = errBox();
    if (el) el.textContent = e && e.message ? e.message : String(e || "");
  }

  function requireAuth() {
    if (document.body.dataset.page === "login") return;
    fetch("/api/v1/status", { headers: headers() }).then((r) => {
      if (r.status === 401) location.href = prefix + "/login";
      else {
        const btn = $("#logout-btn");
        if (btn && token) btn.hidden = false;
      }
    }).catch(() => {
      location.href = prefix + "/login";
    });
  }

  function bindChrome() {
    const theme = $("#theme-btn");
    if (theme) {
      theme.onclick = () => document.documentElement.classList.toggle("light");
    }
    const logout = $("#logout-btn");
    if (logout) {
      logout.onclick = () => {
        token = "";
        localStorage.removeItem(TOKEN_KEY);
        location.href = prefix + "/login";
      };
    }
  }

  async function pageDashboard() {
    const [st, vlans] = await Promise.all([api("/status"), api("/vlans")]);
    $("#status").innerHTML = [
      ["Версия", st.version],
      ["Активные lease", st.leases_active],
      ["Подсети", st.subnets],
      ["VLAN", st.vlans],
    ].map(([k, v]) => `<div class="card"><div class="muted">${k}</div><strong>${v}</strong></div>`).join("");
    $("#vlans").innerHTML = (vlans.items || []).map((v) => {
      const pct = Math.round((v.utilization || 0) * 100);
      return `<div><div class="muted">${v.name || v.id} · VLAN ${v.id} · ${v.pool_used || 0}/${v.pool_size || 0}</div>
        <div class="bar"><span style="width:${pct}%"></span></div></div>`;
    }).join("") || "<p class='muted'>Нет VLAN</p>";
  }

  async function pageLeases() {
    const form = $("#filter");
    const load = async () => {
      const fd = new FormData(form);
      const q = new URLSearchParams({ limit: "200" });
      if (fd.get("q")) q.set("q", fd.get("q"));
      if (fd.get("vlan")) q.set("vlan", fd.get("vlan"));
      const data = await api("/leases?" + q);
      const rows = data.items || [];
      $("#rows").innerHTML = rows.map((l) =>
        `<tr><td>${l.ip}</td><td>${l.mac}</td><td>${l.vlan_id ?? ""}</td><td>${l.subnet_id || ""}</td>
         <td>${l.giaddr || ""}</td><td>${l.state}</td>
         <td><button type="button" class="danger" data-del="${l.ip}">Удалить</button></td></tr>`
      ).join("");
      $("#csv").onclick = () => {
        const csv = ["ip,mac,vlan,subnet,giaddr,state", ...rows.map((l) =>
          [l.ip, l.mac, l.vlan_id || "", l.subnet_id || "", l.giaddr || "", l.state].join(",")
        )].join("\n");
        const a = document.createElement("a");
        a.href = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
        a.download = "leases.csv";
        a.click();
      };
    };
    form.onsubmit = (e) => { e.preventDefault(); load().catch(showError); };
    $("#rows").onclick = async (e) => {
      const ip = e.target.dataset && e.target.dataset.del;
      if (!ip) return;
      await api("/leases/" + encodeURIComponent(ip), { method: "DELETE" });
      await load();
    };
    await load();
  }

  async function pageSubnets() {
    const load = async () => {
      const data = await api("/subnets");
      $("#rows").innerHTML = (data.items || []).map((s) =>
        `<tr><td>${s.id}</td><td>${s.network}</td><td>${s.range_start || ""} – ${s.range_end || ""}</td>
         <td>${s.vlan_id ?? ""}</td><td><button type="button" class="danger" data-del="${s.id}">Удалить</button></td></tr>`
      ).join("");
    };
    $("#create").onsubmit = async (e) => {
      e.preventDefault();
      const fd = new FormData(e.target);
      const range = String(fd.get("range") || "").split(",").map((x) => x.trim()).filter(Boolean);
      await api("/subnets", {
        method: "POST",
        body: JSON.stringify({
          id: fd.get("id"),
          network: fd.get("network"),
          range,
          gateway: fd.get("gateway"),
        }),
      });
      e.target.reset();
      await load();
    };
    $("#rows").onclick = async (e) => {
      const id = e.target.dataset && e.target.dataset.del;
      if (!id) return;
      await api("/subnets/" + encodeURIComponent(id), { method: "DELETE" });
      await load();
    };
    await load();
  }

  async function pageReservations() {
    const load = async () => {
      const data = await api("/reservations");
      $("#rows").innerHTML = (data.items || []).map((r) =>
        `<tr><td>${r.id}</td><td>${r.mac || ""}</td><td>${r.ip}</td><td>${r.subnet_id}</td>
         <td><button type="button" class="danger" data-del="${r.id}">Удалить</button></td></tr>`
      ).join("");
    };
    $("#create").onsubmit = async (e) => {
      e.preventDefault();
      const fd = new FormData(e.target);
      await api("/reservations", {
        method: "POST",
        body: JSON.stringify({
          mac: fd.get("mac"),
          ip: fd.get("ip"),
          subnet_id: fd.get("subnet_id"),
          hostname: fd.get("hostname"),
        }),
      });
      e.target.reset();
      await load();
    };
    $("#rows").onclick = async (e) => {
      const id = e.target.dataset && e.target.dataset.del;
      if (!id) return;
      await api("/reservations/" + encodeURIComponent(id), { method: "DELETE" });
      await load();
    };
    await load();
  }

  async function pageVlans() {
    const load = async () => {
      const data = await api("/vlans");
      $("#list").innerHTML = (data.items || []).map((v) => {
        const pct = Math.round((v.utilization || 0) * 100);
        return `<div class="card"><strong>${v.id}</strong> ${v.name || ""}
          <span class="muted">${v.interface || ""} → ${v.subnet_ref || ""}</span>
          <div class="bar"><span style="width:${pct}%"></span></div>
          <span class="muted">${v.pool_used || 0}/${v.pool_size || 0}</span>
          <button type="button" class="danger" data-del="${v.id}">Удалить</button></div>`;
      }).join("");
    };
    $("#create").onsubmit = async (e) => {
      e.preventDefault();
      const fd = new FormData(e.target);
      await api("/vlans", {
        method: "POST",
        body: JSON.stringify({
          id: Number(fd.get("id")),
          name: fd.get("name"),
          subnet_ref: fd.get("subnet_ref"),
          priority: Number(fd.get("priority") || 0),
        }),
      });
      e.target.reset();
      await load();
    };
    $("#list").onclick = async (e) => {
      const id = e.target.dataset && e.target.dataset.del;
      if (!id) return;
      await api("/vlans/" + encodeURIComponent(id), { method: "DELETE" });
      await load();
    };
    await load();
  }

  async function pageRelays() {
    const load = async () => {
      const [data, parsers] = await Promise.all([api("/relays"), api("/parsers/circuit-id")]);
      $("#parsers").innerHTML = (parsers.items || []).map((p) =>
        `<option value="${p.name}">${p.name}</option>`
      ).join("");
      $("#rows").innerHTML = (data.items || []).map((r) =>
        `<tr><td>${r.giaddr}</td><td>${r.remote_id || ""}</td><td>${r.trusted}</td>
         <td><button type="button" class="danger" data-del="${r.giaddr}">Удалить</button></td></tr>`
      ).join("");
    };
    $("#create").onsubmit = async (e) => {
      e.preventDefault();
      const fd = new FormData(e.target);
      await api("/relays", {
        method: "POST",
        body: JSON.stringify({ giaddr: fd.get("giaddr"), remote_id: fd.get("remote_id") }),
      });
      e.target.reset();
      await load();
    };
    $("#test").onsubmit = async (e) => {
      e.preventDefault();
      const fd = new FormData(e.target);
      const res = await api("/parsers/circuit-id/test", {
        method: "POST",
        body: JSON.stringify({ parser: fd.get("parser"), circuit_id: fd.get("circuit_id") }),
      });
      $("#out").textContent = JSON.stringify(res, null, 2);
    };
    $("#rows").onclick = async (e) => {
      const id = e.target.dataset && e.target.dataset.del;
      if (!id) return;
      await api("/relays/" + encodeURIComponent(id), { method: "DELETE" });
      await load();
    };
    await load();
  }

  async function pageConfig() {
    const res = await fetch("/api/v1/config", { headers: headers() });
    if (res.status === 401) {
      location.href = prefix + "/login";
      return;
    }
    $("#yaml").value = await res.text();
    $("#check").onclick = async () => {
      try {
        const r = await fetch("/api/v1/config/validate", {
          method: "POST",
          headers: headers({ "Content-Type": "application/yaml" }),
          body: $("#yaml").value,
        });
        if (!r.ok) throw new Error(((await r.json()).error || {}).message || r.statusText);
        $("#msg").textContent = "Схема корректна";
      } catch (e) {
        $("#msg").textContent = e.message;
      }
    };
    $("#save").onclick = async () => {
      const r = await fetch("/api/v1/config", {
        method: "PUT",
        headers: headers({ "Content-Type": "application/yaml" }),
        body: $("#yaml").value,
      });
      $("#msg").textContent = r.ok ? "Применено" : (((await r.json()).error || {}).message || r.statusText);
    };
    $("#reload").onclick = async () => {
      await api("/config/reload", { method: "POST" });
      location.reload();
    };
  }

  function pageLogs() {
    const el = $("#log");
    const q = token ? "?access_token=" + encodeURIComponent(token) : "";
    const es = new EventSource("/api/v1/logs/stream" + q);
    es.onmessage = (ev) => {
      el.textContent += ev.data + "\n";
      el.scrollTop = el.scrollHeight;
    };
    es.onerror = () => {
      showError(new Error("поток журнала оборвался"));
    };
  }

  async function pageUsers() {
    const load = async () => {
      const data = await api("/users");
      $("#list").innerHTML = (data.items || []).map((u) =>
        `<li>${u.username} <span class="muted">${u.role}</span>
         <button type="button" class="danger" data-del="${u.username}">Удалить</button></li>`
      ).join("");
    };
    $("#create").onsubmit = async (e) => {
      e.preventDefault();
      const fd = new FormData(e.target);
      await api("/users", {
        method: "POST",
        body: JSON.stringify({
          username: fd.get("username"),
          password: fd.get("password"),
          role: fd.get("role"),
        }),
      });
      e.target.reset();
      await load();
    };
    $("#list").onclick = async (e) => {
      const id = e.target.dataset && e.target.dataset.del;
      if (!id) return;
      await api("/users/" + encodeURIComponent(id), { method: "DELETE" });
      await load();
    };
    await load();
  }

  function pageLogin() {
    $("#login").onsubmit = async (e) => {
      e.preventDefault();
      const fd = new FormData(e.target);
      try {
        const res = await fetch("/api/v1/auth/login", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            username: fd.get("username"),
            password: fd.get("password"),
          }),
        });
        const tok = await res.json();
        if (!tok.access_token) throw new Error((tok.error && tok.error.message) || "отказ");
        token = tok.access_token;
        localStorage.setItem(TOKEN_KEY, token);
        location.href = prefix + "/";
      } catch (err) {
        showError(err);
      }
    };
  }

  const pages = {
    dashboard: pageDashboard,
    leases: pageLeases,
    subnets: pageSubnets,
    reservations: pageReservations,
    vlans: pageVlans,
    relays: pageRelays,
    config: pageConfig,
    logs: pageLogs,
    users: pageUsers,
    login: pageLogin,
  };

  document.addEventListener("DOMContentLoaded", () => {
    bindChrome();
    const page = document.body.dataset.page;
    if (page !== "login") requireAuth();
    const fn = pages[page];
    if (!fn) return;
    Promise.resolve(fn()).catch(showError);
  });
})();
