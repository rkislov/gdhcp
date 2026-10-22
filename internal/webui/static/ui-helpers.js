/* Copyright 2026 Кислов Роман Сергеевич. Apache-2.0. */
(function (root) {
  "use strict";
  const escapeHTML = value => String(value ?? "").replace(/[&<>"']/g, char => ({"&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;"})[char]);
  function csvCell(value) {
    let text = String(value ?? "");
    if (/^\s*[=+@-]|^[\t\r\n]/.test(text)) text = "'" + text;
    return '"' + text.replace(/"/g, '""') + '"';
  }
  const csv = (columns, rows) => "\uFEFF" + [columns.map(([title]) => csvCell(title)).join(","), ...rows.map(row => columns.map(([, key]) => csvCell(row[key])).join(","))].join("\r\n");
  const states = {bound: {label: "Активна", tone: "success"}, offered: {label: "Предложена", tone: "info"}, declined: {label: "Конфликт IP", tone: "danger"}, released: {label: "Освобождена", tone: "neutral"}, expired: {label: "Истекла", tone: "neutral"}};
  const state = value => Object.hasOwn(states, value) ? states[value] : {label: String(value || "Неизвестно"), tone: "neutral"};
  const ranks = {admin: 3, operator: 2, viewer: 1};
  const rank = role => Object.hasOwn(ranks, role) ? ranks[role] : 0;
  function uptime(seconds) {
    const n = Math.max(0, Math.floor(Number(seconds) || 0));
    if (n >= 86400) return `${Math.floor(n / 86400)} д ${Math.floor(n % 86400 / 3600)} ч`;
    if (n >= 3600) return `${Math.floor(n / 3600)} ч ${Math.floor(n % 3600 / 60)} мин`;
    return `${Math.floor(n / 60)} мин`;
  }
  const helpers = {escapeHTML, csvCell, csv, state, rank, uptime};
  if (typeof module !== "undefined" && module.exports) module.exports = helpers;
  if (root) {
    root.GoDHCPUI = helpers;
    try { document.documentElement.classList.toggle("dark", localStorage.getItem("godhcp_theme") === "dark"); } catch (_) {}
  }
})(typeof window !== "undefined" ? window : null);
