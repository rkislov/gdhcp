// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0

const {test} = require("node:test");
const assert = require("node:assert/strict");
const {escapeHTML, csv, state, rank, uptime, normalizeListResponse} = require("../static/ui-helpers.js");

test("API text cannot inject tags or attributes", () => {
  assert.equal(escapeHTML('<img src=x onerror="alert(1)">'), '&lt;img src=x onerror=&quot;alert(1)&quot;&gt;');
  assert.equal(escapeHTML("' & < >"), "&#39; &amp; &lt; &gt;");
  assert.equal(escapeHTML(null), "");
});
test("CSV preserves commas, quotes and newlines and neutralizes formulas", () => {
  const output = csv([["Имя", "name"]], [{name: 'desk, "one"\nline'}, {name: '=HYPERLINK("https://example.com")'}, {name: '  +1'}, {name: '@SUM(A1)'}]);
  assert.ok(output.startsWith('\uFEFF"Имя"\r\n'));
  assert.ok(output.includes('"desk, ""one""\nline"'));
  assert.ok(output.includes('"\'=HYPERLINK(""https://example.com"")"'));
  assert.ok(output.includes('"\'  +1"'));
  assert.ok(output.includes('"\'@SUM(A1)"'));
});
test("CSV exports every IPv4 octet and the complete IPv6 address as quoted fields", () => {
  const addresses = ["10.0.116.37", "192.168.10.51", "0.0.0.0", "255.255.255.255", "2001:db8::100"];
  const rows = addresses.map(ip => ({ip, mac: "00:58:3f:29:bd:75"}));
  assert.equal(csv([["IP", "ip"], ["MAC", "mac"]], rows),
    '\uFEFF"IP","MAC"\r\n' + addresses.map(ip => `"${ip}","00:58:3f:29:bd:75"`).join("\r\n"));
});
test("unknown states and roles have no elevated meaning", () => {
  assert.equal(state("bound").label, "Активна");
  assert.equal(state("constructor").tone, "neutral");
  assert.equal(rank("operator"), 2);
  assert.equal(rank("constructor"), 0);
  assert.equal(rank("__proto__"), 0);
});
test("uptime shows meaningful durations", () => {
  assert.equal(uptime(90), "1 мин");
  assert.equal(uptime(3660), "1 ч 1 мин");
  assert.equal(uptime(90000), "1 д 1 ч");
});

test("null list responses are safe to render without changing other API responses", () => {
  const original = {items: null, total: 0};
  const normalized = normalizeListResponse(original);
  assert.deepEqual(normalized.items.map(x => x), []);
  assert.equal(normalized.total, 0);
  assert.equal(original.items, null);
  const populated = {items: [{name: "cisco"}]};
  assert.equal(normalizeListResponse(populated), populated);
  const status = {role: "admin"};
  assert.equal(normalizeListResponse(status), status);
  assert.equal(normalizeListResponse(null), null);
});

test("CSV export loads every filtered page instead of the 200 displayed rows", async () => {
  const {exportLeases} = require("../static/ui-helpers.js");
  const items = Array.from({length: 201}, (_, i) => ({ip: `10.0.116.${i + 1}`}));
  items.push({ip: "10.0.117.2"}, {ip: "10.0.118.2"});
  const query = new URLSearchParams("limit=200&offset=200&sort=ip&order=desc&vlan=116&state=bound&q=10.0.");
  const output = await exportLeases(async path => {
    const params = new URL(path, "http://localhost").searchParams;
    assert.equal(params.get("all"), "true");
    assert.equal(params.has("offset"), false); assert.equal(params.has("limit"), false);
    for (const key of ["sort", "order", "vlan", "state", "q"]) assert.equal(params.get(key), query.get(key));
    return {items, total: items.length};
  }, query);
  assert.equal(output.split("\r\n").length, 204);
  assert.ok(output.includes('"10.0.117.2"')); assert.ok(output.includes('"10.0.118.2"'));
  assert.equal(query.get("offset"), "200");
  await assert.rejects(exportLeases(async () => ({items: items.slice(0,200), total:203}),query), /неполный/);
  await assert.rejects(exportLeases(async () => {throw new Error("Access denied");},query), /Access denied/);
});
