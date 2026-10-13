// Users dashboard: who signed up, who's active, which apps they use. Readable only by the
// admin emails in quicknotes.config.json (enforced by firestore.rules).

import { barChart } from "./charts.js";

const demo = new URLSearchParams(location.search).has("demo") && /^(localhost|127\.0\.0\.1)$/.test(location.hostname);
const { createStore } = await import(demo ? "./store-demo.js" : "./store-firebase.js");
const store = createStore();

const DAY = 86400000;
const $ = (id) => document.getElementById(id);
const body = $("admin-body");

function h(tag, attrs = {}, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v == null || v === false) continue;
    if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "class") el.className = v;
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children.flat()) if (c != null && c !== false) el.append(c instanceof Node ? c : String(c));
  return el;
}

const fmtDate = (d) => (d ? d.toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" }) : "–");
function ago(d) {
  if (!d) return "never";
  const mins = Math.round((Date.now() - d) / 60000);
  if (mins < 2) return "just now";
  if (mins < 60) return `${mins} min ago`;
  const hrs = Math.round(mins / 60);
  if (hrs < 24) return `${hrs} h ago`;
  const days = Math.round(hrs / 24);
  return days === 1 ? "yesterday" : `${days} days ago`;
}
const within = (d, days) => d && Date.now() - d < days * DAY;
const pad = (n) => String(n).padStart(2, "0");
const isoOf = (d) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;

function appsOf(u) {
  const c = u.clients || {};
  const apps = [];
  if (c.web) apps.push(c.web.mobile ? "Phone" : "Web");
  if (c.mac) apps.push("Mac");
  if (c.chrome) apps.push("Chrome");
  return apps;
}

let users = [];
let sort = { key: "lastSeenAt", dir: -1 };
let filter = "";

function render() {
  const total = users.length;
  const tile = (label, value, sub) => h("div", { class: "stat" },
    h("span", { class: "stat-label" }, label), h("b", { class: "stat-value" }, value), sub ? h("span", { class: "stat-sub" }, sub) : null);
  const newWeek = users.filter((u) => within(u.createdAt, 7)).length;
  const activeDay = users.filter((u) => within(u.lastSeenAt, 1)).length;
  const activeWeek = users.filter((u) => within(u.lastSeenAt, 7)).length;
  const notes = users.reduce((sum, u) => sum + (u.notesCreated || 0), 0);

  // sign-ups per day, last 30 days
  const days = [...Array(30)].map((_, i) => { const d = new Date(Date.now() - (29 - i) * DAY); return isoOf(d); });
  const signups = days.map((d) => users.filter((u) => u.createdAt && isoOf(u.createdAt) === d).length);
  const label = (iso) => new Date(`${iso}T12:00`).toLocaleDateString(undefined, { month: "short", day: "numeric" });

  const appNames = ["Web", "Phone", "Mac", "Chrome"];
  const appCounts = appNames.map((a) => users.filter((u) => appsOf(u).includes(a)).length);

  body.replaceChildren(
    h("div", { class: "stats" },
      tile("Users", total, `${newWeek} new in the last 7 days`),
      tile("Active today", activeDay, total ? `${Math.round((activeDay / total) * 100)}% of users` : ""),
      tile("Active this week", activeWeek, total ? `${Math.round((activeWeek / total) * 100)}% of users` : ""),
      tile("Notes created", notes, total ? `${(notes / total).toFixed(1)} per user` : "")),
    h("div", { class: "admin-charts" },
      h("section", { class: "panel" }, h("h2", { class: "group-title" }, "Sign-ups per day · last 30 days"),
        barChart({
          title: "New users per day, last 30 days", categories: days.map(label), series: [{ name: "Sign-ups", values: signups }],
          axisLabels: days.map((d, i) => (i % 7 === 2 || i === 29 ? label(d) : "")),
        })),
      h("section", { class: "panel" }, h("h2", { class: "group-title" }, "Users by app"),
        barChart({ title: "Number of users who have used each app", categories: appNames, series: [{ name: "Users", values: appCounts }], width: 340 }))),
    table());
}

function table() {
  const q = filter.toLowerCase();
  const rows = users
    .filter((u) => !q || `${u.displayName} ${u.email}`.toLowerCase().includes(q))
    .sort((a, b) => {
      const va = a[sort.key] ?? 0, vb = b[sort.key] ?? 0;
      if (typeof va === "string") return va.localeCompare(vb) * sort.dir;
      return ((va instanceof Date ? va.getTime() : va) - (vb instanceof Date ? vb.getTime() : vb)) * sort.dir;
    });

  const th = (label, key) => h("th", {},
    key ? h("button", {
      class: `sort${sort.key === key ? " on" : ""}`, "aria-sort": sort.key === key ? (sort.dir > 0 ? "ascending" : "descending") : "none",
      onclick: () => { sort = { key, dir: sort.key === key ? -sort.dir : -1 }; render(); },
    }, label, sort.key === key ? (sort.dir > 0 ? " ↑" : " ↓") : "") : label);

  const search = h("input", {
    type: "search", placeholder: "Search name or email", "aria-label": "Search users", value: filter, "data-keep": "admin-search",
    oninput: (e) => { filter = e.target.value; const pos = e.target.selectionStart; render(); const s = document.querySelector('[data-keep="admin-search"]'); s.focus(); s.setSelectionRange(pos, pos); },
  });

  return h("section", { class: "panel admin-table" },
    h("div", { class: "admin-table-head" },
      h("h2", { class: "group-title" }, "All users ", h("span", { class: "n" }, rows.length)),
      search,
      h("button", { class: "ghost-btn accent", onclick: exportCsv }, "Export CSV")),
    h("div", { class: "table-scroll" },
      h("table", {},
        h("thead", {}, h("tr", {}, th("User", "displayName"), th("Joined", "createdAt"), th("Last seen", "lastSeenAt"),
          th("Notes", "notesCreated"), th("Web visits", "visits"), th("Apps"), th("Time zone", "timeZone"))),
        h("tbody", {}, rows.map((u) => h("tr", {},
          h("td", {}, h("div", { class: "who" },
            h("span", { class: "avatar" }, u.photoURL ? h("img", { src: u.photoURL, alt: "", referrerpolicy: "no-referrer" }) : (u.displayName || u.email || "?").charAt(0).toUpperCase()),
            h("div", {}, h("b", {}, u.displayName || "–"), h("small", {}, u.email || "")))),
          h("td", {}, fmtDate(u.createdAt)),
          h("td", { title: u.lastSeenAt ? u.lastSeenAt.toLocaleString() : "" }, ago(u.lastSeenAt)),
          h("td", { class: "num" }, u.notesCreated || 0),
          h("td", { class: "num" }, u.visits || 0),
          h("td", {}, appsOf(u).map((a) => h("span", { class: "app-badge" }, a))),
          h("td", {}, u.timeZone || "–")))))));
}

function exportCsv() {
  const cells = (row) => row.map((v) => `"${String(v ?? "").replace(/"/g, '""')}"`).join(",");
  const lines = [cells(["Name", "Email", "Joined", "Last seen", "Notes created", "Web visits", "Apps", "Time zone", "Locale"])];
  for (const u of users) {
    lines.push(cells([u.displayName, u.email, u.createdAt?.toISOString(), u.lastSeenAt?.toISOString(), u.notesCreated || 0,
      u.visits || 0, appsOf(u).join(" "), u.timeZone, u.locale]));
  }
  const a = h("a", { href: URL.createObjectURL(new Blob([lines.join("\n")], { type: "text/csv" })), download: `quicknotes-users-${isoOf(new Date())}.csv` });
  a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}

function message(title, text, link) {
  body.replaceChildren(h("div", { class: "empty" }, h("strong", {}, title), text, link ? h("p", {}, link) : null));
}

store.onAuth(async (user) => {
  if (user === undefined) return;
  if (!user) return message("Sign in first", "Sign in on the notes page, then come back here.", h("a", { href: "./" }, "Go to QuickNotes"));
  try {
    users = await store.listUsers();
    render();
  } catch (e) {
    if (e?.code === "permission-denied") {
      message("Admins only", `${user.email} isn’t on the admin list. Admins are set in quicknotes.config.json (adminEmails), then deployed.`);
    } else {
      message("Couldn’t load users", e?.message || String(e));
    }
  }
});
