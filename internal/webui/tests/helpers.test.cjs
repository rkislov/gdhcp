// Copyright 2026 Кислов Роман Сергеевич
// SPDX-License-Identifier: Apache-2.0

const {test} = require("node:test");
const assert = require("node:assert/strict");
const {escapeHTML, csv, state, rank, uptime} = require("../static/ui-helpers.js");

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
