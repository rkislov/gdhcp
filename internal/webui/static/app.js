/* Copyright 2026 Кислов Роман Сергеевич. Apache-2.0. */
(function () {
  "use strict";
  const {escapeHTML: esc, csv, state, rank, uptime} = window.GoDHCPUI;
  const $ = (selector, root = document) => root.querySelector(selector);
  const prefix = document.body.dataset.uiPrefix || "/ui";
  const page = document.body.dataset.page;
  const roles = {admin: "Администратор", operator: "Оператор", viewer: "Наблюдатель"};
  const key = "godhcp_session";
  let session = {}, persistent = false, principal = {role: "viewer", username: ""};
  let pageLoader, refreshPromise, toastTimer, logCleanup;
  try {
    const value = sessionStorage.getItem(key) || localStorage.getItem(key);
    if (value) { session = JSON.parse(value); persistent = !!localStorage.getItem(key); }
    else session.access_token = localStorage.getItem("godhcp_token") || "";
  } catch (_) {}
  const can = role => rank(principal.role) >= rank(role);
  function clearSession() {
    session = {};
    try { sessionStorage.removeItem(key); localStorage.removeItem(key); localStorage.removeItem("godhcp_token"); } catch (_) {}
  }
  function storeSession(tokens, remember = persistent) {
    clearSession(); session = tokens; persistent = remember;
    try { (remember ? localStorage : sessionStorage).setItem(key, JSON.stringify(tokens)); } catch (_) {}
  }
  function headers(extra = {}) { return {...(session.access_token ? {Authorization: "Bearer " + session.access_token} : {}), ...extra}; }
  async function refreshSession() {
    if (!session.refresh_token) throw new Error("Сеанс завершён. Войдите снова.");
    if (!refreshPromise) refreshPromise = (async () => {
      const response = await fetch("/api/v1/auth/refresh", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({refresh_token: session.refresh_token})});
      const tokens = await response.json();
      if (!response.ok || !tokens.access_token) throw new Error("Не удалось обновить сеанс.");
      storeSession(tokens);
    })().finally(() => { refreshPromise = null; });
    return refreshPromise;
  }
  async function api(path, options = {}, retry = true) {
    const {raw = false, ...request} = options;
    const response = await fetch("/api/v1" + path, {...request, headers: headers({"Content-Type": "application/json", ...request.headers})});
    if (response.status === 401) {
      if (retry && session.refresh_token) {
        try { await refreshSession(); return await api(path, options, false); } catch (_) {}
      }
      clearSession(); location.assign(prefix + "/login");
      throw new Error("Сеанс завершён. Войдите снова.");
    }
    if (!response.ok) {
      const body = await response.json().catch(() => ({}));
      throw new Error(body.error?.message || (response.status === 403 ? "Недостаточно прав для этого действия." : "Ошибка сервера: " + response.status));
    }
    if (response.status === 204) return null;
    return raw ? response.text() : response.json();
  }
  function showError(error) { if ($("#err")) $("#err").textContent = error?.message || String(error); }
  function toast(message) {
    const el = $("#toast"); el.textContent = message; el.hidden = false;
    clearTimeout(toastTimer); toastTimer = setTimeout(() => { el.hidden = true; }, 4500);
  }
  async function busy(button, action) {
    if (button?.disabled) return;
    if (button) { button.disabled = true; button.setAttribute("aria-busy", "true"); }
    if ($("#err")) $("#err").textContent = "";
    try { return await action(); } catch (error) { showError(error); }
    finally { if (button) { button.disabled = false; button.removeAttribute("aria-busy"); } }
  }
  function confirmAction(title, description, label = "Удалить") {
    const dialog = $("#confirm-dialog");
    $("#confirm-title").textContent = title; $("#confirm-description").textContent = description; $("#confirm-ok").textContent = label;
    return new Promise(resolve => {
      const finish = value => { dialog.close(); resolve(value); };
      $("#confirm-ok").onclick = () => finish(true); $("#confirm-cancel").onclick = () => finish(false);
      dialog.oncancel = event => { event.preventDefault(); finish(false); };
      dialog.showModal(); $("#confirm-cancel").focus();
    });
  }
  function badge(value) {
    const info = state(value);
    return `<span class="badge ${info.tone === "neutral" ? "" : "badge-" + info.tone}">${esc(info.label)}</span>`;
  }
  function date(value) {
    const parsed = new Date(value);
    return !value || Number.isNaN(parsed.valueOf()) ? "—" : parsed.toLocaleString("ru-RU", {day: "2-digit", month: "short", hour: "2-digit", minute: "2-digit"});
  }
  const empty = (title, hint) => `<div class="empty-state"><strong>${esc(title)}</strong>${esc(hint)}</div>`;
  function tableRows(id, html, columns, title = "Записей пока нет", hint = "Добавьте первую запись с помощью формы выше.") {
    $("#" + id).innerHTML = html || `<tr><td colspan="${columns}">${empty(title, hint)}</td></tr>`;
  }
  function count(value) { if ($("#table-count")) $("#table-count").textContent = "Всего: " + value.toLocaleString("ru-RU"); }
  function deleteButton(id, name, min = "admin", disabled = false) {
    return can(min) ? `<button type="button" class="btn-quiet text-red-600 hover:bg-red-50 hover:text-red-700 dark:text-red-400" data-del="${esc(id)}" data-name="${esc(name)}" ${disabled ? 'disabled title="Нельзя удалить последнего администратора"' : ""}>Удалить</button>` : "—";
  }
  function form(id, action) {
    const el = $("#" + id);
    if (el) el.onsubmit = event => { event.preventDefault(); busy($("button[type=submit]", el), () => action(new FormData(el))); };
  }
  function created(message) { const el = $("#create"); el.reset(); el.closest("details")?.removeAttribute("open"); toast(message); }
  function bindDelete(id, endpoint, description) {
    $("#" + id).onclick = event => {
      const button = event.target.closest("button[data-del]");
      if (button) busy(button, async () => {
        if (!await confirmAction(`Удалить «${button.dataset.name}»?`, description)) return;
        await api(endpoint + "/" + encodeURIComponent(button.dataset.del), {method: "DELETE"});
        await pageLoader(); toast("Запись удалена");
      });
    };
  }
  async function fillSubnets() {
    const el = $("#subnet-select"); if (!el) return;
    const previous = el.value, {items = []} = await api("/subnets");
    el.innerHTML = '<option value="">Выберите подсеть</option>' + items.map(s => `<option value="${esc(s.id)}">${esc(s.id)} · ${esc(s.network)}</option>`).join("");
    el.value = previous;
  }
  function download(content, name, type) {
    const url = URL.createObjectURL(new Blob([content], {type}));
    const anchor = document.createElement("a"); anchor.href = url; anchor.download = name; anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
  function connected(ok) {
    if ($("#connection-dot")) $("#connection-dot").className = "size-1.5 rounded-full " + (ok ? "bg-emerald-500" : "bg-red-500");
    if ($("#connection-text")) $("#connection-text").textContent = ok ? "API доступен" : "Нет связи с API";
  }
  async function authorize() {
    const status = await api("/status"); principal = {role: status.role || "viewer", username: status.username || "Пользователь"};
    $("#user-name").textContent = principal.username; $("#user-role").textContent = roles[principal.role] || principal.role;
    $("#user-initial").textContent = principal.username.charAt(0).toUpperCase(); $("#logout-btn").hidden = !session.access_token;
    document.querySelectorAll("[data-role-min]").forEach(el => { el.hidden = !can(el.dataset.roleMin); }); connected(true);
    const min = document.body.dataset.rolePage;
    if (min && !can(min)) { $("#main").innerHTML = '<h1>Доступ ограничен</h1>' + empty("Нужны права администратора", "Выберите доступный раздел в меню или обратитесь к администратору."); return false; }
    return true;
  }
  function dashboard() {
    return async () => {
      const [s, vlans, leases] = await Promise.all([api("/status"), api("/vlans"), api("/leases?limit=6")]);
      const stats = [["Занято адресов", s.leases_active, "Аренды, резервации и удержания"], ["Подсети", s.subnets, "Настроено на сервере"], ["VLAN", s.vlans, "Сегментов в конфигурации"], ["Время работы", uptime(s.uptime_seconds), "С момента запуска процесса"]];
      $("#status").innerHTML = stats.map(([label, value, hint], i) => `<div class="stat-card"><div><span class="stat-label">${label}</span><strong class="stat-value">${esc(value)}</strong><span class="mt-3 block text-[10px] text-slate-400">${hint}</span></div><span class="stat-icon">${["IP", "NET", "802.1Q", "UP"][i]}</span></div>`).join("");
      $("#vlans").innerHTML = vlans.items.map(v => {
        const pct = Math.min(100, Math.max(0, Math.round((v.utilization || 0) * 100)));
        return `<div class="px-5 py-5"><div class="mb-3 flex items-start justify-between gap-3"><div class="flex min-w-0 items-start gap-3"><span class="badge badge-info">${esc(v.id)}</span><div class="min-w-0"><div class="truncate text-[13px] font-medium">${esc(v.name || "VLAN " + v.id)}</div><div class="mt-1 text-[11px] text-slate-400">${esc(v.subnet_ref || "Подсеть не задана")} ${v.interface ? "· " + esc(v.interface) : ""}</div></div></div><div class="shrink-0 text-right text-xs"><span class="font-medium tabular-nums">${v.pool_used || 0}</span><span class="text-slate-400"> / ${v.pool_size || 0} адресов</span></div></div><div class="flex items-center gap-4"><div class="progress ${pct >= 100 ? "full" : pct >= 80 ? "high" : ""}"><span style="width:${pct}%"></span></div><span class="w-9 text-right text-[10px] tabular-nums text-slate-400">${pct}%</span></div></div>`;
      }).join("") || empty("VLAN пока не настроены", "Добавьте сегменты в разделе VLAN или в конфигурации.");
      const details = [["Версия", "v" + s.version], ["База данных", s.database === "sqlite" ? "SQLite" : "PostgreSQL"], ["Режим", s.authoritative ? "Authoritative" : "Non-authoritative"], ["Ваш доступ", roles[principal.role] || principal.role]];
      $("#server-details").innerHTML = details.map(([k, v]) => `<div class="definition-row"><dt>${k}</dt><dd>${esc(v)}</dd></div>`).join("");
      tableRows("recent-leases", leases.items.map(l => `<tr><td class="mono font-medium">${esc(l.ip)}</td><td><div class="text-xs">${esc(l.hostname || "Без имени")}</div><div class="mono mt-1 text-slate-400">${esc(l.mac)}</div></td><td class="text-xs text-slate-500">${esc(l.subnet_id)}</td><td>${badge(l.state)}</td><td class="whitespace-nowrap text-xs text-slate-500">${esc(date(l.expires_at))}</td></tr>`).join(""), 5, "Аренд пока нет", "Здесь появятся устройства после первого DHCP-запроса.");
    };
  }
  function leasesPage() {
    let offset = 0, rows = [], limit = 50, total = 0;
    const load = async () => {
      const values = new FormData($("#filter")); limit = Number(values.get("limit")) || 50;
      const query = new URLSearchParams({limit, offset});
      for (const k of ["q", "vlan", "state"]) if (values.get(k)) query.set(k, values.get(k));
      const data = await api("/leases?" + query); rows = data.items; total = data.total;
      if (offset >= total && offset > 0) { offset = Math.max(0, Math.floor(Math.max(0, total - 1) / limit) * limit); return load(); }
      tableRows("rows", rows.map(l => `<tr><td class="mono font-medium">${esc(l.ip)}</td><td><div class="text-xs">${esc(l.hostname || "Без имени")}</div><div class="mono mt-1 text-slate-400">${esc(l.mac)}</div></td><td>${l.vlan_id ? `<span class="badge">${esc(l.vlan_id)}</span>` : "—"}</td><td class="text-xs text-slate-500">${esc(l.subnet_id)}</td><td class="mono text-slate-500">${esc(l.giaddr || "—")}</td><td>${badge(l.state)}</td><td class="whitespace-nowrap text-xs text-slate-500">${esc(date(l.expires_at))}</td><td>${deleteButton(l.ip, l.ip, "operator")}</td></tr>`).join(""), 8, "Аренды не найдены", "Попробуйте изменить фильтры или дождитесь DHCP-запросов.");
      count(total); $("#page-range").textContent = total ? `${offset + 1}–${offset + rows.length} из ${total}` : "Нет записей";
      $("#page-number").textContent = Math.floor(offset / limit) + 1; $("#prev-page").disabled = offset === 0; $("#next-page").disabled = offset + limit >= total;
    };
    form("filter", async () => { offset = 0; await load(); });
    $("#prev-page").onclick = () => busy($("#refresh-btn"), async () => { offset = Math.max(0, offset - limit); await load(); });
    $("#next-page").onclick = () => busy($("#refresh-btn"), async () => { offset += limit; await load(); });
    $("#csv").onclick = () => download(csv([["IP", "ip"], ["MAC", "mac"], ["Имя", "hostname"], ["VLAN", "vlan_id"], ["Подсеть", "subnet_id"], ["Состояние", "state"], ["Истекает", "expires_at"]], rows), "godhcp-leases.csv", "text/csv;charset=utf-8");
    bindDelete("rows", "/leases", "Адрес будет освобождён на сервере. Устройство может использовать его до конца аренды. Убедитесь, что удаление безопасно."); return load;
  }
  function subnetsPage() {
    const load = async () => {
      const {items} = await api("/subnets"); count(items.length);
      tableRows("rows", items.map(s => `<tr><td class="font-medium">${esc(s.id)}</td><td class="mono">${esc(s.network)}</td><td class="mono text-slate-500">${s.range_start ? esc(s.range_start) + " — " + esc(s.range_end) : "Только резервации"}</td><td class="mono text-slate-500">${esc(s.gateway || "—")}</td><td>${s.vlan_id ? `<span class="badge">${esc(s.vlan_id)}</span>` : "—"}</td><td>${deleteButton(s.id, s.id)}</td></tr>`).join(""), 6);
    };
    form("create", async v => {
      const body = {id: v.get("id").trim(), network: v.get("network").trim(), range: v.get("range").split(",").map(s => s.trim()).filter(Boolean), gateway: v.get("gateway").trim(), dns: v.get("dns").split(",").map(s => s.trim()).filter(Boolean)};
      if (v.get("vlan")) body.vlan = Number(v.get("vlan"));
      await api("/subnets", {method: "POST", body: JSON.stringify(body)}); created("Подсеть добавлена"); await load();
    });
    bindDelete("rows", "/subnets", "Подсеть будет удалена из конфигурации. Сервер перестанет выдавать адреса её клиентам."); return load;
  }
  function reservationsPage() {
    const load = async () => {
      const [{items}] = await Promise.all([api("/reservations"), fillSubnets()]); count(items.length);
      tableRows("rows", items.map(r => `<tr><td class="mono font-medium">${esc(r.ip)}</td><td class="mono text-slate-500">${esc(r.mac || r.client_id)}</td><td>${esc(r.hostname || "—")}</td><td class="text-xs text-slate-500">${esc(r.subnet_id)}</td><td>${deleteButton(r.id, r.ip, "operator")}</td></tr>`).join(""), 5);
    };
    form("create", async v => {
      const body = Object.fromEntries(v); Object.keys(body).forEach(k => { body[k] = body[k].trim(); });
      if (!body.mac && !body.client_id) throw new Error("Укажите MAC-адрес или Client ID.");
      if (!body.subnet_id) throw new Error("Выберите подсеть.");
      if (body.client_id && !/^(?:[0-9a-f]{2})+$/i.test(body.client_id)) throw new Error("Client ID задаётся hex-строкой, например 01001122aabbcc.");
      await api("/reservations", {method: "POST", body: JSON.stringify(body)}); created("Резервация добавлена"); await load();
    });
    bindDelete("rows", "/reservations", "Постоянное закрепление IP будет удалено. Проверьте устройство перед повторной выдачей адреса."); return load;
  }
  function vlansPage() {
    const load = async () => {
      const [{items}] = await Promise.all([api("/vlans"), fillSubnets()]); count(items.length);
      tableRows("list", items.map(v => {
        const pct = Math.min(100, Math.max(0, Math.round(v.utilization * 100 || 0)));
        return `<tr><td><span class="badge badge-info">${esc(v.id)}</span></td><td class="font-medium">${esc(v.name || "—")}</td><td class="mono text-slate-500">${esc(v.interface || "—")}</td><td class="text-xs text-slate-500">${esc(v.subnet_ref || "—")}</td><td class="min-w-44"><div class="mb-2 flex justify-between text-[11px] text-slate-500"><span>${v.pool_used} / ${v.pool_size}</span><span>${pct}%</span></div><div class="progress ${pct >= 80 ? "high" : ""}"><span style="width:${pct}%"></span></div></td><td>${deleteButton(v.id, "VLAN " + v.id)}</td></tr>`;
      }).join(""), 6);
    };
    form("create", async v => {
      if (!v.get("subnet_ref")) throw new Error("Выберите подсеть для VLAN.");
      const result = await api("/vlans", {method: "POST", body: JSON.stringify({id: Number(v.get("id")), name: v.get("name").trim(), interface: v.get("interface").trim(), subnet_ref: v.get("subnet_ref"), priority: Number(v.get("priority") || 0)})});
      created("VLAN добавлен"); if (result.link_warning) showError(new Error(result.link_warning)); await load();
    });
    bindDelete("list", "/vlans", "Привязка VLAN будет удалена из конфигурации. Проверьте зависимые подсети и клиентов."); return load;
  }
  function relaysPage() {
    const load = async () => {
      const [{items}, parsers] = await Promise.all([api("/relays"), api("/parsers/circuit-id"), fillSubnets()]); count(items.length);
      tableRows("rows", items.map(r => `<tr><td class="mono font-medium">${esc(r.giaddr)}</td><td class="mono text-slate-500">${esc(r.remote_id || "—")}</td><td><span class="badge ${r.trusted ? "badge-success" : "badge-warning"}">${r.trusted ? "Доверенный" : "Обнаружен"}</span></td><td class="text-xs text-slate-500">${esc(r.subnet_id || "—")}</td><td>${r.trusted ? deleteButton(r.giaddr, r.giaddr) : "—"}</td></tr>`).join(""), 5);
      const selected = $("#parsers").value; $("#parsers").innerHTML = parsers.items.map(p => `<option value="${esc(p.name)}">${esc(p.name)}</option>`).join("");
      if (selected) $("#parsers").value = selected;
    };
    form("create", async v => { await api("/relays", {method: "POST", body: JSON.stringify(Object.fromEntries(v))}); created("Relay добавлен в доверенные"); await load(); });
    form("test", async v => {
      const r = await api("/parsers/circuit-id/test", {method: "POST", body: JSON.stringify(Object.fromEntries(v))});
      $("#out").hidden = false; $("#out").textContent = r.ok ? `Распознано: VLAN ${r.vlan}\nПарсер: ${r.parser}\n\n` + JSON.stringify(r.fields, null, 2) : "Совпадение не найдено. Проверьте Circuit ID и выбранный парсер.";
    });
    bindDelete("rows", "/relays", "Relay будет удалён из доверенных. Проверьте, что оставшиеся правила доверия соответствуют вашей сети."); return load;
  }
  function configPage() {
    let original = ""; const editor = $("#yaml"), message = $("#msg");
    function editorState() {
      const dirty = editor.value !== original; $("#editor-status").textContent = dirty ? "Есть изменения" : "Без изменений";
      $("#editor-status").className = "badge " + (dirty ? "badge-warning" : "badge-success"); $("#editor-lines").textContent = `${editor.value.split("\n").length} строк · YAML · UTF-8`;
    }
    const load = async () => {
      if (editor.value !== original && !await confirmAction("Отбросить изменения?", "В редактор будет загружена активная конфигурация.", "Загрузить")) return;
      original = await api("/config", {raw: true}); editor.value = original; editorState();
    };
    editor.oninput = editorState;
    editor.onkeydown = e => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") { e.preventDefault(); $("#save").click(); }
      if (e.key === "Tab") { e.preventDefault(); editor.setRangeText("  ", editor.selectionStart, editor.selectionEnd, "end"); editorState(); }
    };
    async function validate() {
      await api("/config/validate", {method: "POST", headers: {"Content-Type": "application/yaml"}, body: editor.value});
      message.className = "text-sm text-emerald-700 dark:text-emerald-400"; message.textContent = "Конфигурация прошла проверку.";
    }
    $("#check").onclick = () => busy($("#check"), validate);
    $("#save").onclick = () => busy($("#save"), async () => {
      await validate(); if (!await confirmAction("Применить конфигурацию?", "Настройки подсетей и пулов вступят в силу сразу. Параметры слушателей и БД изменятся после перезапуска.", "Применить")) return;
      await api("/config", {method: "PUT", headers: {"Content-Type": "application/yaml"}, body: editor.value}); original = editor.value; editorState(); toast("Конфигурация применена");
    });
    $("#reload").onclick = () => busy($("#reload"), async () => {
      if (!await confirmAction("Перечитать файл сервера?", "Применятся настройки с диска. Изменения в редакторе будут потеряны.", "Перечитать")) return;
      await api("/config/reload", {method: "POST"}); original = editor.value; await load(); toast("Конфигурация перечитана");
    });
    $("#export-config").onclick = () => busy($("#export-config"), async () => { download(await api("/config", {raw: true}), "godhcp-config-redacted.yml", "application/yaml"); toast("YAML экспортирован. JWT и API-ключи скрыты."); });
    return load;
  }
  function usersPage() {
    const descriptions = {admin: "Настройки сервера и управление доступом", operator: "Аренды, резервации и диагностика", viewer: "Просмотр состояния и журналов"};
    const load = async () => {
      const {items} = await api("/users"); count(items.length); const admins = items.filter(u => u.role === "admin").length;
      tableRows("list", items.map(u => `<tr><td><div class="flex items-center gap-3"><span class="flex size-8 items-center justify-center rounded-full bg-slate-100 text-xs font-medium text-slate-600 dark:bg-slate-800 dark:text-slate-300">${esc(u.username.charAt(0).toUpperCase())}</span><span class="font-medium">${esc(u.username)}</span>${u.username === principal.username ? '<span class="badge badge-info">Вы</span>' : ""}</div></td><td><span class="badge">${esc(roles[u.role] || u.role)}</span></td><td class="text-xs text-slate-500">${esc(descriptions[u.role] || "—")}</td><td>${deleteButton(u.username, u.username, "admin", u.role === "admin" && admins === 1)}</td></tr>`).join(""), 4);
    };
    form("create", async v => { await api("/users", {method: "POST", body: JSON.stringify(Object.fromEntries(v))}); created("Пользователь создан"); await load(); });
    bindDelete("list", "/users", "Учётная запись будет удалена. Ранее выданные JWT продолжают действовать: для срочного отзыва смените секрет сервера."); return load;
  }
  function logsPage() {
    let source, records = [], paused = false, reconnecting = false;
    const pane = $("#log"), status = $("#stream-status");
    function render() {
      if (paused) return;
      const q = $("#log-filter").value.toLowerCase(), selected = records.filter(line => line.toLowerCase().includes(q));
      const fragment = document.createDocumentFragment();
      for (const line of selected) {
        const el = document.createElement("div"); el.className = "log-line"; el.textContent = line;
        try { const d = JSON.parse(line); if (d.level === "ERROR") el.classList.add("text-red-400"); else if (d.level === "WARN") el.classList.add("text-amber-400"); } catch (_) {}
        fragment.appendChild(el);
      }
      pane.replaceChildren(fragment); if (!selected.length) pane.innerHTML = empty("Событий нет", q ? "Измените фильтр журнала." : "Новые события появятся автоматически.");
      $("#log-count").textContent = `Показано ${selected.length} из ${records.length} · до 1000 событий`;
      if ($("#autoscroll").checked) pane.scrollTop = pane.scrollHeight;
    }
    function connect() {
      source?.close(); status.textContent = "Подключение…"; status.className = "badge";
      source = new EventSource("/api/v1/logs/stream" + (session.access_token ? "?access_token=" + encodeURIComponent(session.access_token) : ""));
      source.onopen = () => { status.textContent = paused ? "Пауза" : "Прямой эфир"; status.className = "badge badge-success"; };
      source.onmessage = e => { records.push(e.data.slice(0, 16000)); if (records.length > 1000) records.shift(); render(); };
      source.onerror = async () => {
        status.textContent = "Восстановление связи…"; status.className = "badge badge-warning";
        if (session.refresh_token && !reconnecting) {
          reconnecting = true; source.close();
          try { await refreshSession(); connect(); } catch (e) { showError(e); status.textContent = "Войдите снова"; }
          finally { reconnecting = false; }
        }
      };
    }
    $("#pause-logs").onclick = () => { paused = !paused; $("#pause-logs").textContent = paused ? "Продолжить" : "Пауза"; status.textContent = paused ? "Пауза" : "Прямой эфир"; render(); };
    $("#clear-logs").onclick = () => { records = []; const was = paused; paused = false; render(); paused = was; };
    $("#log-filter").oninput = render; logCleanup = () => source?.close(); return async () => { connect(); };
  }
  function loginPage() {
    const password = $("#password");
    $("#password-toggle").onclick = () => { password.type = password.type === "password" ? "text" : "password"; $("#password-toggle").setAttribute("aria-label", password.type === "password" ? "Показать пароль" : "Скрыть пароль"); };
    password.onkeyup = e => { $("#caps-lock").hidden = !e.getModifierState("CapsLock"); };
    form("login", async v => {
      const response = await fetch("/api/v1/auth/login", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({username: v.get("username").trim(), password: v.get("password")})});
      const tokens = await response.json();
      if (!response.ok || !tokens.access_token) throw new Error(tokens.error?.message === "invalid credentials" ? "Неверное имя пользователя или пароль." : tokens.error?.message || "Не удалось войти в панель.");
      storeSession(tokens, v.get("remember") === "on"); location.assign(prefix + "/");
    });
  }
  function bindChrome() {
    if ($("#theme-btn")) $("#theme-btn").onclick = () => {
      const dark = document.documentElement.classList.toggle("dark"); try { localStorage.setItem("godhcp_theme", dark ? "dark" : "light"); } catch (_) {}
    };
    const menu = $("#menu-btn"), overlay = $("#sidebar-overlay");
    if (menu) {
      const narrow = matchMedia("(max-width: 1023px)"), sidebar = $("#sidebar");
      function setMenu(open) {
        sidebar.classList.toggle("open", open); overlay.hidden = !open;
        menu.setAttribute("aria-expanded", String(open));
        sidebar.inert = narrow.matches && !open;
        $("#main").inert = narrow.matches && open;
        document.body.style.overflow = narrow.matches && open ? "hidden" : "";
        if (open) $("a", sidebar).focus();
        else if (sidebar.contains(document.activeElement)) menu.focus();
      }
      setMenu(false);
      narrow.addEventListener("change", () => setMenu(false));
      menu.onclick = () => setMenu(menu.getAttribute("aria-expanded") !== "true");
      overlay.onclick = () => setMenu(false);
      document.addEventListener("keydown", e => { if (e.key === "Escape") setMenu(false); });
    }
    if ($("#logout-btn")) $("#logout-btn").onclick = () => { clearSession(); location.assign(prefix + "/login"); };
  }
  const pages = {dashboard, leases: leasesPage, subnets: subnetsPage, reservations: reservationsPage, vlans: vlansPage, relays: relaysPage, config: configPage, logs: logsPage, users: usersPage};
  async function refreshPage() {
    try {
      await pageLoader(); connected(true);
      $("#page-meta").textContent = "Обновлено в " + new Date().toLocaleTimeString("ru-RU", {hour: "2-digit", minute: "2-digit"});
    } catch (error) { connected(false); throw error; }
  }
  async function start() {
    bindChrome(); if (page === "login") { loginPage(); return; }
    if (!await authorize() || !pages[page]) return;
    pageLoader = pages[page](); $("#refresh-btn").onclick = () => busy($("#refresh-btn"), refreshPage); await refreshPage();
  }
  window.addEventListener("pagehide", () => logCleanup?.());
  start().catch(error => { connected(false); showError(error); });
})();
