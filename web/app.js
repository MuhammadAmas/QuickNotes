import { MAX_CONTENT, normalizeEmails } from "./notes-model.js";

// ---------- data source: Firestore, or an in-memory demo on localhost?demo ----------

const demo = new URLSearchParams(location.search).has("demo") && /^(localhost|127\.0\.0\.1)$/.test(location.hostname);
const { createStore } = await import(demo ? "./store-demo.js" : "./store-firebase.js");
const store = createStore();

// ---------- dates (always the device's local calendar day) ----------

const pad = (n) => String(n).padStart(2, "0");
const isoOf = (d) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
const parseIso = (s) => { const [y, m, d] = s.split("-").map(Number); return new Date(y, m - 1, d); };
const today = () => isoOf(new Date());
const addDays = (iso, n) => { const d = parseIso(iso); d.setDate(d.getDate() + n); return isoOf(d); };

function dayName(iso) {
  const diff = Math.round((parseIso(iso) - parseIso(today())) / 86400000);
  if (diff === 0) return "Today";
  if (diff === -1) return "Yesterday";
  if (diff === 1) return "Tomorrow";
  return parseIso(iso).toLocaleDateString(undefined, { weekday: "long" });
}
const shortDate = (iso) => parseIso(iso).toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" });
const longDate = (iso) => parseIso(iso).toLocaleDateString(undefined, { weekday: "long", month: "long", day: "numeric", year: "numeric" });
// "today" / "tomorrow" / "yesterday", otherwise "Mon, Oct 13"
const dayLabel = (iso) => (["Today", "Tomorrow", "Yesterday"].includes(dayName(iso)) ? dayName(iso).toLowerCase() : shortDate(iso));
// leftovers from past days go to today; today or later goes to the following day
const carryTarget = (iso) => (iso < today() ? today() : addDays(iso, 1));
const timeOf = (ts) => new Date(ts).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
const plural = (n, word) => `${n} ${word}${n === 1 ? "" : "s"}`;
// priority: the order set by dragging; notes never dragged fall back to when they were added
const rank = (n) => (typeof n.order === "number" ? n.order : Date.parse(n.created_at) || 0);
const byRank = (a, b) => rank(a) - rank(b);

// ---------- small helpers ----------

const prefs = {
  get(key, fallback) { try { return localStorage.getItem(key) ?? fallback; } catch { return fallback; } },
  set(key, value) { try { localStorage.setItem(key, value); } catch { /* private mode etc. */ } },
};

const $ = (id) => document.getElementById(id);

function h(tag, attrs = {}, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v == null || v === false) continue;
    if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "class") el.className = v;
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

function icon(name) {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("class", "icon");
  svg.setAttribute("aria-hidden", "true");
  const use = document.createElementNS("http://www.w3.org/2000/svg", "use");
  use.setAttribute("href", `#i-${name}`);
  svg.append(use);
  return svg;
}

// ---------- links in note text ----------

const URL_RE = /\b(?:https?:\/\/|www\.)[^\s<>"']+/gi;

/** Note text as DOM nodes, with web addresses turned into links that open in a new tab. */
function linkify(text) {
  const out = [];
  let last = 0;
  for (const match of text.matchAll(URL_RE)) {
    let url = match[0];
    // leave sentence punctuation (and an unmatched closing bracket) outside the link
    url = url.replace(/[.,!?;:'"]+$/, "");
    while (/[)\]}]$/.test(url) && (url.match(/[(\[{]/g) || []).length < (url.match(/[)\]}]/g) || []).length) url = url.slice(0, -1);
    let href;
    try {
      href = new URL(url.startsWith("www.") ? `https://${url}` : url);
    } catch { continue; }
    if (href.protocol !== "http:" && href.protocol !== "https:") continue;

    out.push(text.slice(last, match.index));
    const shown = url.replace(/^https?:\/\//, "").replace(/\/$/, "");
    out.push(h("a", {
      class: "note-link", href: href.href, target: "_blank", rel: "noopener noreferrer", title: href.href,
      onclick: (e) => e.stopPropagation(), ondblclick: (e) => e.stopPropagation(),
    }, shown.length > 60 ? `${shown.slice(0, 57)}…` : shown));
    last = match.index + url.length;
  }
  out.push(text.slice(last));
  return out.filter((part) => part !== "");
}

function avatar(name, photo, cls = "avatar") {
  const el = h("span", { class: cls, "aria-hidden": "true" });
  if (photo) el.append(h("img", { src: photo, alt: "", referrerpolicy: "no-referrer" }));
  else el.textContent = (name || "?").trim().charAt(0).toUpperCase();
  return el;
}

function friendly(e) {
  if (e?.code === "permission-denied") return "You don’t have permission to change that note.";
  if (e?.code === "unavailable") return "Can’t reach the server right now. Changes will sync when you’re back online.";
  if (e?.code === "auth/unauthorized-domain") return "This web address isn’t allowed to sign in yet (add it under Firebase Authentication → Settings → Authorized domains).";
  if (e?.code === "auth/operation-not-allowed") return "Google sign-in isn’t switched on for this Firebase project yet.";
  return e?.message || String(e);
}

// ---------- state ----------

const state = {
  user: null,
  notes: [],          // everything I own plus everything shared with me (live)
  loaded: false,
  loadError: null,
  view: "day",        // "day" | "open" | "done" | "shared"
  date: today(),
  query: "",
  editingId: null,
  completedOpen: prefs.get("qn-completed-open", "1") === "1",
};
const pendingDeletes = new Map(); // id -> timeout handle

const isMine = (n) => n.ownerId === state.user?.uid;
const liveNotes = () => state.notes.filter((n) => !pendingDeletes.has(n.id));

function daySummaries() {
  const days = new Map();
  for (const n of liveNotes()) {
    const d = days.get(n.note_date) || { note_date: n.note_date, total: 0, open: 0, done: 0 };
    d.total++;
    d[n.status === "done" ? "done" : "open"]++;
    days.set(n.note_date, d);
  }
  return [...days.values()].sort((a, b) => (a.note_date < b.note_date ? 1 : -1));
}

function notesForView() {
  const all = liveNotes();
  if (state.query) {
    const q = state.query.toLowerCase();
    return all.filter((n) => n.content.toLowerCase().includes(q) || (n.ownerName || "").toLowerCase().includes(q));
  }
  switch (state.view) {
    case "open": return all.filter((n) => n.status === "open");
    case "done": return all.filter((n) => n.status === "done");
    case "shared": return all.filter((n) => !isMine(n) || n.sharedWith?.length);
    default: return all.filter((n) => n.note_date === state.date);
  }
}

// ---------- theme ----------

function applyTheme(choice) {
  const root = document.documentElement;
  if (choice === "light" || choice === "dark") root.dataset.theme = choice;
  else delete root.dataset.theme;
  for (const b of document.querySelectorAll("[data-theme-choice]")) {
    b.setAttribute("aria-checked", String(b.dataset.themeChoice === choice));
  }
}

// ---------- routing (kept in the URL hash so reloads stay put) ----------

function readHash() {
  const [, view, arg] = location.hash.split("/");
  if (["open", "done", "shared"].includes(view)) state.view = view;
  else if (view === "day" && /^\d{4}-\d{2}-\d{2}$/.test(arg || "")) { state.view = "day"; state.date = arg; }
  else { state.view = "day"; state.date = today(); }
}

function go(view, date) {
  state.query = "";
  $("search").value = "";
  state.editingId = null;
  closeDrawer();
  const hash = view === "day" ? `#/day/${date || today()}` : `#/${view}`;
  if (location.hash === hash) { readHash(); render(); } else location.hash = hash;
}

// ---------- rendering ----------

function render() {
  if (!state.user) return;
  if (state.editingId != null && document.querySelector(".edit-box")) return; // don't clobber an edit in progress
  if (drag) return; // or a drag in progress
  renderSidebar();
  renderMain();
  renderShareDialog();
}

function renderAccount() {
  const u = state.user;
  $("account-avatar").replaceWith(Object.assign(avatar(u.name, u.photo), { id: "account-avatar" }));
  $("account-name").textContent = u.name;
  $("account-email").textContent = u.email;
}

function renderSidebar() {
  const t = today();
  const days = daySummaries();
  const todayRow = days.find((d) => d.note_date === t);
  const all = liveNotes();
  $("count-today").textContent = todayRow?.open || "";
  $("count-open").textContent = all.filter((n) => n.status === "open").length || "";
  $("count-done").textContent = all.filter((n) => n.status === "done").length || "";
  $("count-shared").textContent = all.filter((n) => !isMine(n) && n.status === "open").length || "";

  for (const btn of document.querySelectorAll(".nav-item")) {
    const v = btn.dataset.view;
    const active = !state.query && (v === "today" ? state.view === "day" && state.date === t : state.view === v);
    btn.classList.toggle("active", active);
  }

  const list = $("days");
  list.replaceChildren();
  if (state.loaded && !days.length) list.append(h("li", { class: "days-empty" }, "Your days will show up here"));
  for (const d of days) {
    const active = !state.query && state.view === "day" && state.date === d.note_date;
    const right = d.open
      ? h("span", { class: "count", title: `${d.open} open of ${d.total}` }, d.open)
      : h("span", { class: "all-done", title: "All done" }, icon("check"));
    list.append(h("li", {},
      h("button", { class: `day-item${active ? " active" : ""}`, onclick: () => go("day", d.note_date) },
        h("span", { class: "day-label" }, h("b", {}, dayName(d.note_date)), h("small", {}, shortDate(d.note_date))),
        right)));
  }
  $("jump").value = state.view === "day" ? state.date : "";
}

const VIEW_TEXT = {
  open: ["Everything still to do", "All open"],
  done: ["Ticked off", "Completed"],
  shared: ["With you and by you", "Shared"],
};

function renderMain() {
  const t = today();
  const isDay = state.view === "day" && !state.query;
  const visible = notesForView();
  const open = visible.filter((n) => n.status === "open").sort(byRank);
  const done = visible.filter((n) => n.status === "done").sort(byRank);

  $("day-nav").hidden = !isDay;
  $("go-today").disabled = state.date === t;
  $("composer").hidden = !!state.query || state.view === "done" || state.view === "shared";

  if (state.query) {
    $("eyebrow").textContent = "Search";
    $("title").textContent = `“${state.query}”`;
  } else if (isDay) {
    $("eyebrow").textContent = longDate(state.date);
    $("title").textContent = dayName(state.date);
    $("composer-input").placeholder = state.date === t ? "Add a note for today…" : `Add a note for ${shortDate(state.date)}…`;
  } else {
    [$("eyebrow").textContent, $("title").textContent] = VIEW_TEXT[state.view];
    $("composer-input").placeholder = "Add a note for today…";
  }

  // progress bar for a single day
  const total = open.length + done.length;
  $("progress").hidden = !isDay || total === 0;
  if (isDay && total) {
    $("progress-fill").style.width = `${(done.length / total) * 100}%`;
    $("progress").classList.toggle("complete", open.length === 0);
    $("progress-text").textContent = open.length === 0 ? "All done 🎉" : `${done.length} of ${total} done`;
  }

  const list = $("list");
  list.replaceChildren();
  if (!navigator.onLine) list.append(h("div", { class: "banner" }, icon("cloud-off"), "You’re offline. Changes are saved on this device and sync when you reconnect."));
  if (state.loadError) list.append(h("div", { class: "banner" }, `Can’t load notes: ${state.loadError}`));
  if (!state.loaded && !state.loadError) { list.append(h("div", { class: "loading" }, "Loading your notes…")); return; }
  if (!visible.length) { list.append(emptyState()); return; }

  if (isDay) {
    if (open.length) {
      list.append(h("section", {},
        groupHead(h("h2", { class: "group-title" }, "To do", h("span", { class: "n" }, open.length)),
          h("div", { class: "group-actions" },
            h("button", { class: "ghost-btn accent", onclick: () => carryOver(state.date, carryTarget(state.date)) },
              `Move all to ${dayLabel(carryTarget(state.date))}`, icon("arrow-right")))),
        noteList(open, true)));
    } else {
      list.append(h("div", { class: "caught-up" }, icon("check-circle"), "All caught up for this day."));
    }
    if (done.length) {
      const toggle = h("button", {
        class: "group-title collapse-btn", "aria-expanded": String(state.completedOpen),
        onclick: () => {
          state.completedOpen = !state.completedOpen;
          prefs.set("qn-completed-open", state.completedOpen ? "1" : "0");
          renderMain();
        },
      }, icon("chevron-down"), "Completed", h("span", { class: "n" }, done.length));
      list.append(h("section", {}, groupHead(toggle), state.completedOpen ? noteList(done) : null));
    }
    return;
  }

  // grouped by day: "open" oldest → newest (overdue first); other views newest first
  const groups = new Map();
  for (const n of [...open, ...done]) {
    if (!groups.has(n.note_date)) groups.set(n.note_date, []);
    groups.get(n.note_date).push(n);
  }
  const dates = [...groups.keys()].sort();
  if (state.query || state.view !== "open") dates.reverse();
  for (const d of dates) {
    const overdue = state.view === "open" && !state.query && d < t;
    list.append(h("section", {},
      groupHead(
        h("h2", { class: "group-title" }, `${dayName(d)} · ${shortDate(d)}`),
        overdue ? h("span", { class: "tag overdue" }, "Overdue") : null,
        h("div", { class: "group-actions" },
          overdue ? h("button", { class: "ghost-btn accent", onclick: () => carryOver(d, t) }, "Move all to today", icon("arrow-right")) : null,
          h("button", { class: "ghost-btn", onclick: () => go("day", d) }, "Open day"))),
      noteList(groups.get(d), state.view === "open" && !state.query)));
  }
}

function groupHead(...children) {
  return h("div", { class: "group-head" }, children);
}

function emptyState() {
  const box = (ic, title, ...text) => h("div", { class: "empty" }, h("div", { class: "empty-icon" }, icon(ic)), h("strong", {}, title), ...text);
  if (state.query) return box("search", "Nothing found", "Try a different word.");
  if (state.view === "open") return box("check-circle", "All clear", "Every note is ticked off. Nice work.");
  if (state.view === "done") return box("check-circle", "Nothing completed yet", "Tick a note and it lands here.");
  if (state.view === "shared") return box("users", "Nothing shared yet", "Use the share button on a note to send it to someone.");
  if (state.date === today()) {
    return box("pencil", "No notes yet today",
      "Type above, or click the floating bubble on your Mac (", h("kbd", {}, "⌘⇧N"), ").");
  }
  return box("calendar", "No notes on this day", "Type above to add one.");
}

function noteList(notes, draggable = false) {
  return h("ul", { class: "notes" }, notes.map((n) => noteItem(n, draggable && n.status === "open")));
}

function noteItem(n, draggable) {
  const done = n.status === "done";
  const mine = isMine(n);
  const moveTo = carryTarget(n.note_date);
  const moveLabel = `Move to ${dayLabel(moveTo)}`;
  const shares = n.sharedWith || [];

  const meta = [h("span", { class: "chip", title: new Date(n.created_at).toLocaleString() }, icon("clock"), timeOf(n.created_at))];
  if (!mine) {
    meta.push(h("span", { class: "chip person", title: n.ownerEmail }, avatar(n.ownerName, "", "avatar tiny"), `from ${n.ownerName || n.ownerEmail}`));
  } else if (shares.length) {
    meta.push(h("span", { class: "chip shared", title: `Shared with ${shares.join(", ")}`, onclick: () => openShare(n.id) },
      icon("users"), `shared with ${shares.length === 1 ? shares[0].split("@")[0] : `${shares.length} people`}`));
  }
  if (n.times_moved) {
    meta.push(h("span", { class: "chip moved", title: `Moved ${n.times_moved}× since ${shortDate(n.original_date)}` },
      icon("redo"), `from ${shortDate(n.original_date)}`));
  }
  if (n.edited_at) meta.push(h("span", { class: "chip edited", title: new Date(n.edited_at).toLocaleString() }, icon("pencil"), `updated ${timeOf(n.edited_at)}`));
  if (done && n.completed_at) meta.push(h("span", { class: "chip donechip" }, icon("check"), `done ${timeOf(n.completed_at)}`));

  const editing = state.editingId === n.id;
  const shot = SAFE_IMAGE.test(n.attachment?.thumb || "") ? n.attachment : null;
  const body = editing ? editor(n) : h("div", {},
    h("div", { class: "note-text", ondblclick: () => startEdit(n.id), title: "Double-click to edit" }, linkify(n.content)),
    shot ? h("button", { class: "shot", title: "View screenshot", "aria-label": "View screenshot", onclick: () => openShot(n) },
      h("img", { src: shot.thumb, alt: "", loading: "lazy" })) : null,
    h("div", { class: "meta" }, meta));

  return h("li", { class: `note${done ? " done" : ""}`, "data-id": n.id },
    draggable ? h("button", {
      class: "drag-handle", title: "Drag to set priority (or focus and press ↑ ↓)", "aria-label": "Reorder note",
      onpointerdown: (e) => startDrag(e, n), onkeydown: (e) => nudge(e, n),
    }, icon("grip")) : null,
    h("button", {
      class: "check", role: "checkbox", "aria-checked": String(done),
      "aria-label": done ? "Mark as not done" : "Mark as done",
      onclick: () => setStatus(n, done ? "open" : "done"),
    }, icon("check")),
    body,
    h("div", { class: "note-actions" },
      h("button", { class: "icon-btn", title: "Edit", "aria-label": "Edit note", onclick: () => startEdit(n.id) }, icon("pencil")),
      done ? null : h("button", { class: "icon-btn go", title: moveLabel, "aria-label": moveLabel, onclick: () => moveNote(n, moveTo) }, icon("arrow-right")),
      mine ? h("button", { class: "icon-btn share", title: "Share", "aria-label": "Share note", onclick: () => openShare(n.id) }, icon("share")) : null,
      mine ? h("button", { class: "icon-btn danger", title: "Delete", "aria-label": "Delete note", onclick: () => deleteNote(n) }, icon("trash")) : null));
}

// ---------- drag to reorder ----------

let drag = null;

/** Moves DOM nodes and animates the other rows sliding into their new places (FLIP). */
function slide(list, mutate) {
  const rows = [...list.children].filter((el) => !el.classList.contains("dragging"));
  const before = new Map(rows.map((el) => [el, el.getBoundingClientRect().top]));
  mutate();
  for (const el of rows) {
    const dy = before.get(el) - el.getBoundingClientRect().top;
    if (!dy) continue;
    el.style.transition = "none";
    el.style.transform = `translateY(${dy}px)`;
    requestAnimationFrame(() => {
      el.style.transition = "transform .18s ease";
      el.style.transform = "";
      el.addEventListener("transitionend", () => { el.style.transition = ""; }, { once: true });
    });
  }
}

function startDrag(e, note) {
  if (e.button !== 0) return;
  e.preventDefault();
  const handle = e.currentTarget;
  const li = handle.closest("li");
  const list = li.parentElement;
  handle.setPointerCapture(e.pointerId);

  // positions relative to the list, so scrolling mid-drag doesn't throw them off.
  // The DOM isn't touched until the drop (moving the row would drop the pointer capture).
  const listTop = () => list.getBoundingClientRect().top;
  const rows = [...list.children].filter((el) => el.querySelector(".drag-handle"));
  const start = rows.indexOf(li);
  const top0 = listTop();
  const slots = rows.map((el) => { const r = el.getBoundingClientRect(); return { el, top: r.top - top0, mid: r.top - top0 + r.height / 2 }; });
  const step = li.getBoundingClientRect().height + 1; // row height + 1px divider
  const grab = e.clientY - li.getBoundingClientRect().top;
  let index = start;

  drag = { note };
  li.classList.add("dragging");
  document.body.classList.add("is-dragging");
  for (const el of rows) if (el !== li) el.style.transition = "transform .18s ease";

  const scroller = document.querySelector(".main");
  const onMove = (ev) => {
    const y = ev.clientY;
    const first = slots[0].top, last = slots[slots.length - 1].top;
    const wanted = y - listTop() - grab;
    li.style.transform = `translateY(${Math.min(Math.max(wanted, first), last) - slots[start].top}px)`;

    // slide the rows the dragged one has passed, and work out where it would land
    // (judged from the pointer, so dropping at the very top/bottom edge still counts)
    const myMid = wanted + step / 2;
    index = start;
    slots.forEach((slot, k) => {
      let shift = 0;
      if (k < start && myMid < slot.mid) { shift = step; index--; }
      if (k > start && myMid > slot.mid) { shift = -step; index++; }
      if (slot.el !== li) slot.el.style.transform = shift ? `translateY(${shift}px)` : "";
    });

    if (scroller) {
      if (y < 70) scroller.scrollTop -= 12;
      else if (y > innerHeight - 70) scroller.scrollTop += 12;
    }
  };
  const onEnd = () => {
    if (!drag) return;
    handle.removeEventListener("pointermove", onMove);
    handle.removeEventListener("pointerup", onEnd);
    handle.removeEventListener("pointercancel", onEnd);
    handle.removeEventListener("lostpointercapture", onEnd);
    for (const el of rows) { el.style.transition = ""; el.style.transform = ""; }
    li.classList.remove("dragging");
    document.body.classList.remove("is-dragging");
    drag = null;
    if (index === start) return render();
    const others = rows.filter((el) => el !== li);
    list.insertBefore(li, others[index] || others[others.length - 1].nextSibling);
    commitOrder(list, li, note);
  };
  handle.addEventListener("pointermove", onMove);
  handle.addEventListener("pointerup", onEnd);
  handle.addEventListener("pointercancel", onEnd);
  handle.addEventListener("lostpointercapture", onEnd);
}

/** Keyboard reordering on the grip: ↑ / ↓ move the note one place. */
function nudge(e, note) {
  if (e.key !== "ArrowUp" && e.key !== "ArrowDown") return;
  e.preventDefault();
  e.stopPropagation();
  const li = e.currentTarget.closest("li");
  const list = li.parentElement;
  const sibling = e.key === "ArrowUp" ? li.previousElementSibling : li.nextElementSibling;
  if (!sibling?.querySelector(".drag-handle")) return;
  slide(list, () => list.insertBefore(li, e.key === "ArrowUp" ? sibling : sibling.nextSibling));
  commitOrder(list, li, note);
  requestAnimationFrame(() => document.querySelector(`li[data-id="${note.id}"] .drag-handle`)?.focus());
}

/** Gives the moved note an order between its new neighbours (renumbering the list on a tie). */
function commitOrder(list, li, note) {
  const ids = [...list.children].map((el) => el.dataset.id).filter(Boolean);
  const notes = ids.map((id) => state.notes.find((n) => n.id === id)).filter(Boolean);
  const i = ids.indexOf(note.id);
  const prev = notes[i - 1], next = notes[i + 1];
  let order;
  if (prev && next) order = (rank(prev) + rank(next)) / 2;
  else if (prev) order = rank(prev) + 1000;
  else if (next) order = rank(next) - 1000;
  else return render();

  if ((prev && order <= rank(prev)) || (next && order >= rank(next))) {
    const base = rank(notes[0]);
    run(store.setOrders(notes.map((n, k) => [n, base + k * 1000])));
  } else {
    run(store.update(note, { order }));
  }
  render();
}

function editor(n) {
  const box = h("textarea", { class: "edit-box", "aria-label": "Edit note", maxlength: MAX_CONTENT });
  box.value = n.content;
  let finished = false;
  const finish = (save) => {
    if (finished) return;
    finished = true;
    state.editingId = null;
    const text = box.value.trim();
    if (save && text && text !== n.content) run(store.update(n, { content: text }), "Note updated");
    render();
  };
  box.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); finish(true); }
    if (e.key === "Escape") { e.preventDefault(); finish(false); }
  });
  box.addEventListener("blur", () => finish(true));
  requestAnimationFrame(() => { box.focus(); box.setSelectionRange(box.value.length, box.value.length); });
  return h("div", {}, box, h("div", { class: "edit-hint" }, "Enter to save · Shift+Enter for a new line · Esc to cancel"));
}

// ---------- actions ----------

/** Firestore applies writes locally at once, so show the result now and only report failures. */
function run(promise, message) {
  if (message) toast(message);
  Promise.resolve(promise).catch((e) => toast(friendly(e)));
}

function startEdit(id) {
  state.editingId = id;
  renderMain();
}

function setStatus(n, status) {
  run(store.update(n, { status }));
}

function moveNote(n, to) {
  run(store.update(n, { note_date: to }), `Moved to ${dayLabel(to)}`);
}

function carryOver(from, to) {
  const list = liveNotes().filter((n) => n.note_date === from && n.status === "open");
  if (!list.length) return;
  run(store.updateMany(list, { note_date: to }), `Moved ${plural(list.length, "note")} to ${dayLabel(to)}`);
}

function deleteNote(n) {
  const handle = setTimeout(() => {
    pendingDeletes.delete(n.id);
    run(store.remove(n));
  }, 5000);
  pendingDeletes.set(n.id, handle);
  render();
  toast("Note deleted", "Undo", () => {
    clearTimeout(pendingDeletes.get(n.id));
    pendingDeletes.delete(n.id);
    render();
  });
}

function addNote(content) {
  const noteDate = state.view === "day" && !state.query ? state.date : today();
  run(store.create(content.slice(0, MAX_CONTENT), noteDate));
}

// ---------- screenshots ----------

const SAFE_IMAGE = /^data:image\/(jpeg|png|webp|svg\+xml);base64,/;

async function openShot(note) {
  const dialog = $("shot-dialog");
  const full = $("shot-full");
  full.src = note.attachment.thumb; // blurry stand-in until the full image arrives
  $("shot-caption").textContent = note.content;
  $("shot-loading").hidden = false;
  $("shot-download").removeAttribute("href");
  dialog.showModal();
  try {
    const url = await store.getAttachment(note.attachment.id);
    if (!SAFE_IMAGE.test(url)) throw new Error("Unsupported image");
    if (dialog.open) {
      full.src = url;
      $("shot-download").href = url;
    }
  } catch (e) {
    toast(friendly(e));
  } finally {
    $("shot-loading").hidden = true;
  }
}

$("shot-close").addEventListener("click", () => $("shot-dialog").close());
$("shot-dialog").addEventListener("click", (e) => { if (e.target === e.currentTarget) e.currentTarget.close(); });

// ---------- sharing ----------

let shareNoteId = null;

function openShare(id) {
  shareNoteId = id;
  $("share-error").hidden = true;
  $("share-email").value = "";
  renderShareDialog();
  if (!$("share-dialog").open) $("share-dialog").showModal();
  $("share-email").focus();
}

function renderShareDialog() {
  if (shareNoteId == null || !$("share-dialog").open) return;
  const note = state.notes.find((n) => n.id === shareNoteId);
  if (!note) { $("share-dialog").close(); return; }
  $("share-preview").textContent = note.content;
  const list = $("share-list");
  list.replaceChildren();
  const emails = note.sharedWith || [];
  if (!emails.length) list.append(h("li", { class: "empty-row" }, "Only you can see this note."));
  for (const email of emails) {
    list.append(h("li", {},
      avatar(email), h("span", {}, email),
      h("button", {
        class: "icon-btn", title: "Stop sharing", "aria-label": `Stop sharing with ${email}`,
        onclick: () => run(store.setSharing(note, emails.filter((e) => e !== email)), `Stopped sharing with ${email}`),
      }, icon("x"))));
  }
}

$("share-form").addEventListener("submit", (e) => {
  e.preventDefault();
  const note = state.notes.find((n) => n.id === shareNoteId);
  const email = $("share-email").value.trim().toLowerCase();
  const error = $("share-error");
  error.hidden = true;
  if (!note) return;
  if (!normalizeEmails([email]).length) { error.textContent = "Enter a valid email address."; error.hidden = false; return; }
  if (email === state.user.email) { error.textContent = "That’s you. You already have this note."; error.hidden = false; return; }
  if ((note.sharedWith || []).includes(email)) { error.textContent = "Already shared with that person."; error.hidden = false; return; }
  run(store.setSharing(note, normalizeEmails([...(note.sharedWith || []), email])), `Shared with ${email}`);
  $("share-email").value = "";
});
$("share-close").addEventListener("click", () => $("share-dialog").close());
$("share-dialog").addEventListener("close", () => { shareNoteId = null; });

// ---------- toast ----------

let toastTimer;
function toast(text, actionLabel, action) {
  const el = $("toast"), btn = $("toast-action");
  $("toast-text").textContent = text;
  btn.hidden = !actionLabel;
  btn.textContent = actionLabel || "";
  btn.onclick = () => { el.hidden = true; action && action(); };
  el.hidden = true;
  void el.offsetWidth; // restart the entrance animation
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.hidden = true; }, actionLabel ? 5000 : 2600);
}

// ---------- mobile drawer ----------

function openDrawer() { document.body.classList.add("nav-open"); $("scrim").hidden = false; }
function closeDrawer() { document.body.classList.remove("nav-open"); $("scrim").hidden = true; }

// ---------- auth ----------

let stopNotes = null;

store.onAuth((user, error) => {
  document.body.classList.remove("booting");
  if (error) {
    $("auth-error").textContent = friendly(error);
    $("auth-error").hidden = false;
  }
  if (user === undefined) return;
  stopNotes?.();
  stopNotes = null;
  Object.assign(state, { user, notes: [], loaded: false, loadError: null });
  $("auth").hidden = !!user;
  $("app").hidden = !user;
  if (!user) return;

  renderAccount();
  stopNotes = store.subscribe(
    (notes) => { Object.assign(state, { notes, loaded: true, loadError: null }); render(); },
    (e) => { state.loadError = friendly(e); render(); },
  );
  render();
});

$("google-signin").addEventListener("click", async () => {
  const btn = $("google-signin");
  btn.disabled = true;
  $("auth-error").hidden = true;
  try { await store.signIn(); } catch (e) {
    $("auth-error").textContent = friendly(e);
    $("auth-error").hidden = false;
  } finally { btn.disabled = false; }
});
$("sign-out").addEventListener("click", () => run(store.signOut()));

// ---------- wiring ----------

$("composer").addEventListener("submit", (e) => {
  e.preventDefault();
  const input = $("composer-input");
  const text = input.value.trim();
  if (!text) return;
  input.value = "";
  addNote(text);
});

let searchTimer;
$("search").addEventListener("input", (e) => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => { state.query = e.target.value.trim(); render(); }, 120);
});
$("search").addEventListener("keydown", (e) => {
  if (e.key === "Enter") closeDrawer();
  if (e.key === "Escape") { e.target.value = ""; state.query = ""; e.target.blur(); render(); }
});

$("jump").addEventListener("change", (e) => { if (e.target.value) go("day", e.target.value); });
$("prev").addEventListener("click", () => go("day", addDays(state.date, -1)));
$("next").addEventListener("click", () => go("day", addDays(state.date, 1)));
$("go-today").addEventListener("click", () => go("day", today()));
for (const btn of document.querySelectorAll(".nav-item")) {
  btn.addEventListener("click", () => go(btn.dataset.view === "today" ? "day" : btn.dataset.view, today()));
}
for (const btn of document.querySelectorAll("[data-theme-choice]")) {
  btn.addEventListener("click", () => {
    prefs.set("qn-theme", btn.dataset.themeChoice);
    applyTheme(btn.dataset.themeChoice);
  });
}
$("menu").addEventListener("click", openDrawer);
$("scrim").addEventListener("click", closeDrawer);

document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") closeDrawer();
  if (!state.user || $("share-dialog").open || $("shot-dialog").open) return;
  const typing = e.target.closest("input, textarea");
  if (typing || e.metaKey || e.ctrlKey || e.altKey) return;
  if (e.key === "/") { e.preventDefault(); $("search").focus(); }
  else if (e.key === "n") { e.preventDefault(); $("composer-input").focus(); }
  else if (e.key === "t") go("day", today());
  else if (e.key === "ArrowLeft" && state.view === "day") go("day", addDays(state.date, -1));
  else if (e.key === "ArrowRight" && state.view === "day") go("day", addDays(state.date, 1));
});

window.addEventListener("hashchange", () => { readHash(); render(); });
window.addEventListener("online", render);
window.addEventListener("offline", render);
// keeps "Today" right across midnight
setInterval(() => { if (state.editingId == null) render(); }, 60000);

applyTheme(prefs.get("qn-theme", "system"));
readHash();
