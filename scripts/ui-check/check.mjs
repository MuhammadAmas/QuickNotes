// UI quality check for the QuickNotes web app and Chrome popup (demo data, no account needed).
// Drives a throwaway headless Chrome over the DevTools protocol and measures, for every view in
// light and dark at desktop (1280x820) and phone (375x812) size:
//   - axe-core violations (WCAG 2.0/2.1/2.2 A+AA and best practices; axe is pinned in this folder)
//   - text contrast (axe's color-contrast rule; cases axe can't decide are listed for a human look)
//   - the same axe rules on interactive states: selected row, menus, toast, editor, shortcut list
//   - tap-target sizes on phone: nothing under 24x24 CSS px, primary actions at least 44x44, and
//     the centre of each target actually reaching it (not covered by another element)
//   - how many notes are visible above the fold on the phone's Today view
//   - keyboard triage: j/k select, x tick, m move menu, Backspace trash, and no shortcut while typing
//
//   node scripts/ui-check/check.mjs            # prints a report, exits 1 if any target is missed
//   node scripts/ui-check/check.mjs --json out.json --shots dir   # also saves numbers and screenshots
//
// Needs Google Chrome and Node 22+. Starts its own local servers for web/ and extension/.

import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const ROOT = join(HERE, "..", "..");
const CHROME = process.env.CHROME || "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const AXE = readFileSync(join(HERE, "axe.min.js"), "utf8");
const AXE_SHA = readFileSync(join(HERE, "axe.min.js.sha256"), "utf8").split(/\s+/)[0];
const args = process.argv.slice(2);
const opt = (name) => { const i = args.indexOf(name); return i >= 0 ? args[i + 1] : null; };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

if (createHash("sha256").update(AXE).digest("hex") !== AXE_SHA) {
  console.error("blocked: axe.min.js does not match its pinned SHA-256");
  process.exit(2);
}
if (!existsSync(CHROME)) {
  console.error(`blocked: Chrome not found at ${CHROME} (set CHROME=...)`);
  process.exit(2);
}

// ---------- targets (the bar this check enforces) ----------

export const TARGETS = {
  axeViolations: 0,
  minContrast: 4.5,     // WCAG 1.4.3 AA for normal text
  minTarget: 24,        // WCAG 2.5.8: every control on phone
  primaryTarget: 44,    // Apple HIG / WCAG 2.5.5 AAA for primary actions on phone
  notesAboveFold: 4,    // Today view, 375x812, demo data
};
// Primary actions on phone: the controls used many times a day.
const PRIMARY = [".note > .check", ".cl-check", "#composer-input", "#menu", "#prev", "#next", "#go-today", ".nav-item"];

// ---------- static servers (no caching, like scripts/serve.py) ----------

const TYPES = { ".html": "text/html", ".js": "text/javascript", ".mjs": "text/javascript", ".css": "text/css", ".json": "application/json",
  ".png": "image/png", ".svg": "image/svg+xml", ".webmanifest": "application/manifest+json", ".ico": "image/x-icon" };
function serve(dir) {
  const server = createServer((req, res) => {
    let path = decodeURIComponent(new URL(req.url, "http://x").pathname);
    if (path.endsWith("/")) path += "index.html";
    const file = normalize(join(dir, path));
    if (!file.startsWith(dir) || !existsSync(file)) { res.writeHead(404); res.end(); return; }
    res.writeHead(200, { "Content-Type": TYPES[extname(file)] || "application/octet-stream", "Cache-Control": "no-store" });
    res.end(readFileSync(file));
  });
  return new Promise((ok) => server.listen(0, "127.0.0.1", () => ok(server)));
}

// ---------- Chrome over the DevTools protocol ----------

async function launchChrome() {
  const profile = mkdtempSync(join(tmpdir(), "qn-uicheck-"));
  const port = 9400 + Math.floor(Math.random() * 400);
  const proc = spawn(CHROME, ["--headless=new", `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`, "--hide-scrollbars",
    "--no-first-run", "--no-default-browser-check", "--mute-audio", "--force-device-scale-factor=1"], { stdio: "ignore" });
  let target;
  for (let i = 0; i < 50 && !target; i++) {
    await sleep(100);
    try { target = (await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()).find((t) => t.type === "page"); } catch { /* not up yet */ }
  }
  if (!target) throw new Error("blocked: Chrome did not start");
  const ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((ok, bad) => { ws.onopen = ok; ws.onerror = bad; });
  let id = 0;
  const pending = new Map();
  ws.onmessage = (m) => {
    const msg = JSON.parse(m.data);
    if (msg.id && pending.has(msg.id)) { const p = pending.get(msg.id); pending.delete(msg.id); msg.error ? p.bad(new Error(msg.error.message)) : p.ok(msg.result); }
  };
  const send = (method, params = {}) => new Promise((ok, bad) => { const n = ++id; pending.set(n, { ok, bad }); ws.send(JSON.stringify({ id: n, method, params })); });
  const evaluate = async (expression) => {
    const r = await send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || JSON.stringify(r.exceptionDetails));
    return r.result.value;
  };
  await send("Page.enable");
  await send("Runtime.enable");
  const close = async () => {
    try { ws.close(); } catch {}
    const exited = new Promise((ok) => proc.once("exit", ok));
    proc.kill();
    await Promise.race([exited, sleep(3000)]);
    for (let i = 0; i < 10; i++) { try { rmSync(profile, { recursive: true, force: true }); break; } catch { await sleep(200); } }
  };
  return { send, evaluate, close };
}

async function setViewport(cdp, phone, dark) {
  await cdp.send("Emulation.setDeviceMetricsOverride", phone
    ? { width: 375, height: 812, deviceScaleFactor: 2, mobile: true }
    : { width: 1280, height: 820, deviceScaleFactor: 1, mobile: false });
  await cdp.send("Emulation.setTouchEmulationEnabled", phone ? { enabled: true, maxTouchPoints: 5 } : { enabled: false });
  await cdp.send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: dark ? "dark" : "light" },
    { name: "prefers-reduced-motion", value: "reduce" }] });
}

async function load(cdp, url) {
  // always a fresh document (and a fresh demo store): a same-URL or hash-only navigation would keep the old page
  await cdp.send("Page.navigate", { url: "about:blank" });
  await sleep(50);
  const loaded = new Promise((ok) => { const t = setInterval(async () => { try { if (await cdp.evaluate("document.readyState") === "complete") { clearInterval(t); ok(); } } catch {} }, 50); });
  await cdp.send("Page.navigate", { url });
  await loaded;
  await sleep(700); // demo data + first render
}

// Runs in the page: axe, a contrast sweep over visible text, and target sizes.
const MEASURE = (primary) => `(async () => {
  if (!window.axe) { ${JSON.stringify(AXE)}; eval(${JSON.stringify(AXE)}); }
  const r = await axe.run(document, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa", "best-practice"] } });
  const violations = r.violations.flatMap((v) => v.nodes.map((n) => ({ rule: v.id, target: n.target.join(" "), detail: (n.any[0] || n.all[0] || n.none[0] || {}).message || v.help })));
  const incomplete = r.incomplete.filter((v) => v.id === "color-contrast").flatMap((v) => v.nodes.map((n) => n.target.join(" ") + " :: " + ((n.any[0] || {}).message || "").slice(0, 120)));
  const visible = (e) => { const s = getComputedStyle(e); const b = e.getBoundingClientRect(); return b.width > 0 && b.height > 0 && s.visibility !== "hidden" && s.display !== "none" && +s.opacity > 0.05 && !e.closest("[hidden],[aria-hidden=true]"); };
  const controls = [...document.querySelectorAll("button, a[href], input:not([type=hidden]), textarea, select, [role=checkbox], [role=button], [tabindex]:not([tabindex='-1'])")].filter(visible);
  const primarySel = ${JSON.stringify(primary)}.join(",");
  // WCAG 2.5.8 exempts links inside a sentence (their size is set by the line of text)
  const inline = (e) => e.matches("a") && e.closest(".note-text, .checklist li span, .week-text, p");
  // How much of a centred circle of the target diameter (or the control's own size, if smaller) actually
  // reaches the control, i.e. isn't covered by another element. A circle, so round buttons aren't
  // penalised for their corners.
  const reach = (e, size) => {
    const b = e.getBoundingClientRect();
    // judged only when fully on screen (a person scrolls a control into view before tapping it),
    // and inside its scroll container's visible area
    if (b.top < 0 || b.left < 0 || b.bottom > innerHeight || b.right > innerWidth) return 1;
    for (let p = e.parentElement; p; p = p.parentElement) {
      const o = getComputedStyle(p).overflowY;
      if (o === "auto" || o === "scroll") { const pb = p.getBoundingClientRect(); if (b.top < pb.top || b.bottom > pb.bottom) return 1; }
    }
    const d = Math.min(size, b.width, b.height), cx = b.left + b.width / 2, cy = b.top + b.height / 2;
    let hit = 0, total = 0;
    for (let i = 0; i < 9; i++) for (let j = 0; j < 9; j++) {
      const dx = ((i + 0.5) / 9 - 0.5) * d, dy = ((j + 0.5) / 9 - 0.5) * d;
      if (dx * dx + dy * dy > (d / 2) * (d / 2)) continue;
      const x = cx + dx, y = cy + dy;
      if (x < 0 || y < 0 || x >= innerWidth || y >= innerHeight) continue;
      total++;
      const at = document.elementFromPoint(x, y);
      if (at && (at === e || e.contains(at))) hit++;
    }
    return total ? hit / total : 1;
  };
  const targets = controls.filter((e) => !inline(e)).map((e) => { const b = e.getBoundingClientRect(); const primary = e.matches(primarySel);
    return { w: Math.round(b.width), h: Math.round(b.height), primary, reach: Math.round(100 * reach(e, primary ? 44 : 24)),
    name: (e.getAttribute("aria-label") || e.title || e.placeholder || e.textContent || e.className || e.tagName).trim().replace(/\\s+/g, " ").slice(0, 40) }; });
  const notes = [...document.querySelectorAll(".notes > .note")].filter(visible);
  const aboveFold = notes.filter((n) => n.getBoundingClientRect().bottom <= innerHeight).length;
  return { violations, incomplete, targets, notes: notes.length, aboveFold, overflowX: document.documentElement.scrollWidth > innerWidth + 1 };
})()`;

// ---------- the checks ----------

const VIEWS = ["day", "open", "done", "shared", "week", "summary", "trash"];

async function main() {
  const webDir = join(ROOT, "web"), extDir = join(ROOT, "extension");
  const web = await serve(webDir), ext = await serve(extDir);
  const WEB = `http://127.0.0.1:${web.address().port}`, EXT = `http://127.0.0.1:${ext.address().port}`;
  const cdp = await launchChrome();
  const shots = opt("--shots");
  if (shots) mkdirSync(shots, { recursive: true });
  const results = [];
  try {
    for (const dark of [false, true]) {
      for (const phone of [false, true]) {
        await setViewport(cdp, phone, dark);
        const pages = VIEWS.map((v) => ({ name: v, url: `${WEB}/?demo#/${v === "day" ? "day" : v}` }));
        pages.push({ name: "popup", url: `${EXT}/popup.html` });
        for (const page of pages) {
          if (page.name === "popup" && phone) continue; // the popup is a fixed 360px desktop surface
          await load(cdp, page.url);
          const m = await cdp.evaluate(MEASURE(PRIMARY));
          const label = `${page.name} · ${dark ? "dark" : "light"} · ${phone ? "phone" : "desktop"}`;
          results.push({ label, page: page.name, dark, phone, ...m });
          if (shots) {
            const { data } = await cdp.send("Page.captureScreenshot", { format: "png" });
            writeFileSync(join(shots, `${page.name}-${dark ? "dark" : "light"}-${phone ? "phone" : "desktop"}.png`), Buffer.from(data, "base64"));
          }
        }
      }
    }
    const keyboard = await keyboardTriage(cdp, WEB);
    const states = await statesAxe(cdp, WEB);
    const flows = await existingFlows(cdp, WEB);
    report(results, keyboard, flows, states);
  } finally {
    await cdp.close();
    web.close();
    ext.close();
  }
}

// Keyboard triage on the desktop Today view (demo store, so changes are visible in the DOM).
// Every check asserts a concrete outcome, so none can pass with the feature missing.
async function keyboardTriage(cdp, WEB) {
  const checks = [];
  const check = (name, ok, detail) => checks.push({ name, ok: Boolean(ok), detail });
  const press = async (key, mods = 0) => {
    const named = { Backspace: ["Backspace", 8], Delete: ["Delete", 46], Escape: ["Escape", 27], Enter: ["Enter", 13], Tab: ["Tab", 9] }[key];
    if (key === " ") { // Space: a printable key whose default action activates buttons
      await cdp.send("Input.dispatchKeyEvent", { type: "keyDown", key: " ", code: "Space", text: " ", windowsVirtualKeyCode: 32 });
      await cdp.send("Input.dispatchKeyEvent", { type: "keyUp", key: " ", code: "Space", windowsVirtualKeyCode: 32 });
      await sleep(220); return;
    }
    if (key === "Enter") { // a real Enter carries "\r", which is what activates a focused button
      await cdp.send("Input.dispatchKeyEvent", { type: "keyDown", key: "Enter", code: "Enter", text: "\r", windowsVirtualKeyCode: 13 });
      await cdp.send("Input.dispatchKeyEvent", { type: "keyUp", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13 });
      await sleep(220); return;
    }
    const ev = named ? { key, code: named[0], windowsVirtualKeyCode: named[1] }
      : { key, text: mods ? undefined : key, code: key === "?" ? "Slash" : `Key${key.toUpperCase()}`, windowsVirtualKeyCode: key === "?" ? 191 : key.toUpperCase().charCodeAt(0) };
    await cdp.send("Input.dispatchKeyEvent", { type: named || mods ? "rawKeyDown" : "keyDown", modifiers: mods | (key === "?" ? 8 : 0), ...ev });
    await cdp.send("Input.dispatchKeyEvent", { type: "keyUp", modifiers: mods, ...ev });
    await sleep(220);
  };
  const state = () => cdp.evaluate(`(() => {
    const rows = [...document.querySelectorAll(".notes > .note")];
    const sel = document.querySelector(".notes > .note.selected");
    const menu = document.querySelector(".move-menu");
    const near = (a, b) => a && b && Math.abs(a.top - b.bottom) < 200 && a.left < b.right + 40 && a.right > b.left - 40;
    return {
      selectedIndex: rows.indexOf(sel), selectedId: sel?.dataset.id || null,
      open: document.querySelectorAll(".notes > .note:not(.done)").length, total: rows.length,
      ids: rows.map((r) => r.dataset.id),
      menu: Boolean(menu), menuNearSelected: Boolean(menu && sel && near(menu.getBoundingClientRect(), sel.getBoundingClientRect())),
      focusOnSelected: Boolean(sel && (document.activeElement === sel || sel.contains(document.activeElement))),
      focus: document.activeElement?.className || document.activeElement?.tagName,
      editing: Boolean(document.querySelector(".edit-box")),
      toast: document.querySelector("#toast")?.hidden === false ? document.querySelector("#toast-text").textContent : null,
      keys: Boolean(document.querySelector("#keys-dialog")?.open),
      composer: document.querySelector("#composer-input")?.value ?? null,
    };
  })()`);
  const fresh = async () => { await setViewport(cdp, false, false); await load(cdp, `${WEB}/?demo#/day`); };
  await fresh();
  const s0 = await state();
  await press("j"); const s1 = await state();
  check("j selects the first note and focus follows", s1.selectedIndex === 0 && s1.focusOnSelected, s1);
  await press("j"); const s2 = await state();
  check("j moves to the next note", s2.selectedIndex === 1 && s2.focusOnSelected, s2);
  await press("k"); await press("k"); const s3 = await state();
  check("k moves back and stops at the first note", s3.selectedIndex === 0, s3);
  for (let i = 0; i < s0.total + 2; i++) await press("j");
  const s3b = await state();
  check("j stops at the last note", s3b.selectedIndex === s0.total - 1, s3b);
  for (let i = 0; i < s0.total + 2; i++) await press("k");
  await press("m"); const s4 = await state();
  check("m opens the move menu next to the selected note", s4.menu && s4.menuNearSelected, s4);
  await press("Escape"); const s5 = await state();
  check("Escape closes the menu and focus returns to the note", !s5.menu && s5.focusOnSelected, s5);
  await press("x"); const s6 = await state();
  check("x ticks the selected note", s6.open === s0.open - 1 && s6.focusOnSelected, { before: s0.open, after: s6.open, focus: s6.focus });
  await press("x"); const s6b = await state();
  check("x again unticks it", s6b.open === s0.open, { after: s6b.open });
  await press("e"); const s7 = await state();
  check("e opens the editor", s7.editing, s7);
  await press("Escape"); const s8 = await state();
  check("leaving the editor returns focus to the note", !s8.editing && s8.focusOnSelected, s8);
  await press("Enter"); const s9 = await state();
  check("Enter on the focused note edits it", s9.editing, s9);
  await press("Escape");
  const beforeTrash = await state();
  await press("Backspace"); const s10 = await state();
  check("Backspace trashes the selected note and offers Undo", s10.total === beforeTrash.total - 1 && /Trash/.test(s10.toast || ""), s10);
  await cdp.evaluate(`document.querySelector("#toast-action").click()`); await sleep(300);
  const s11 = await state();
  check("Undo brings it back", s11.total === beforeTrash.total && s11.ids.includes(beforeTrash.selectedId), s11);
  await press("j"); const beforeDel = await state();
  await press("Delete"); const s12 = await state();
  check("Delete also trashes", s12.total === beforeDel.total - 1, { before: beforeDel.total, after: s12.total });
  // owner-only and done-note rules
  await fresh();
  const sharedIndex = await cdp.evaluate(`[...document.querySelectorAll(".notes > .note")].findIndex((n) => n.querySelector(".chip.person"))`);
  for (let i = 0; i <= sharedIndex; i++) await press("j");
  const sh = await state(); await press("Backspace"); const sh2 = await state();
  check("Backspace on someone else's note is refused with a message", sharedIndex >= 0 && sh2.total === sh.total && /owner/i.test(sh2.toast || ""), sh2);
  const doneIndex = await cdp.evaluate(`[...document.querySelectorAll(".notes > .note")].findIndex((n) => n.classList.contains("done"))`);
  await press("Escape");
  for (let i = 0; i <= doneIndex; i++) await press("j");
  await press("m"); const dn = await state();
  check("m on a done note explains instead of moving", doneIndex >= 0 && !dn.menu && /untick/i.test(dn.toast || ""), dn);
  // must not fire: typing anywhere, menus, dialogs, modifiers
  await fresh();
  const base = await state();
  const typingIn = async (selector, label) => {
    await cdp.evaluate(`document.querySelector(${JSON.stringify(selector)}).focus()`);
    await sleep(150);
    for (const k of ["j", "x", "m", "e"]) await press(k);
    await press("Backspace");
    const typed = await cdp.evaluate(`document.activeElement.value`);
    // clear the field (search filters the list) and compare with the starting state
    await cdp.evaluate(`(() => { const el = document.activeElement; el.value = ""; el.dispatchEvent(new Event("input", { bubbles: true })); el.blur(); })()`);
    await sleep(250);
    const st = await state();
    check(`no shortcut while typing in ${label}`, typed === "jxm" && st.open === base.open && st.total === base.total && st.selectedIndex === -1 && !st.menu && !st.editing, { typed, ...st });
  };
  await typingIn("#composer-input", "the composer");
  await typingIn("#search", "search");
  await cdp.evaluate(`document.querySelector(".cl-add-btn")?.click()`); await sleep(200);
  await typingIn(".cl-add input", "the checklist input");
  await fresh();
  await press("j"); await press("j"); const m0 = await state();
  for (const mod of [2, 1, 4]) await press("x", mod); // Ctrl, Alt, Cmd
  const m1 = await state();
  check("Ctrl/Alt/Cmd + key are not shortcuts", m1.open === m0.open, m1);
  await press("m"); const inMenu = await state();
  await press("x");
  const m2 = await state();
  check("keys don't act while a menu is open", inMenu.menu && m2.open === inMenu.open, m2);
  await press("Escape");
  await press("?"); const k1 = await state();
  await press("x"); const k2 = await state();
  check("? opens the shortcut list, and keys don't act behind it", k1.keys && k2.open === k1.open, { k1: k1.keys });
  // ordinary keyboard use inside note rows must be untouched by the shortcuts
  await fresh();
  await cdp.evaluate(`document.querySelectorAll(".notes > .note:not(.done) > .check")[2].focus()`);
  const sp0 = await state(); await press(" "); const sp1 = await state();
  check("Space on a tick in an unselected note ticks it, without selecting the note", sp1.open === sp0.open - 1 && sp1.selectedIndex === -1, { open: [sp0.open, sp1.open], sel: sp1.selectedIndex });
  await fresh();
  await cdp.evaluate(`document.querySelectorAll(".notes > .note")[1].querySelector('[aria-label="Edit note"]').focus()`);
  await press("Enter"); const en = await state();
  check("Enter on a note's Edit button opens the editor", en.editing, en);
  await fresh();
  await cdp.evaluate(`document.querySelectorAll(".notes > .note")[2].querySelector('[aria-label="Share note"]')?.focus()`);
  await press("Enter");
  check("Enter on a note's Share button opens sharing", await cdp.evaluate(`document.querySelector("#share-dialog").open`), {});
  await fresh();
  // Tab forward through the first two rows, then Shift+Tab back: each control once, same order both ways, no selection
  const seq = async (shift, n) => {
    const out = [];
    for (let i = 0; i < n; i++) {
      await cdp.send("Input.dispatchKeyEvent", { type: "rawKeyDown", key: "Tab", code: "Tab", windowsVirtualKeyCode: 9, modifiers: shift ? 8 : 0 });
      await cdp.send("Input.dispatchKeyEvent", { type: "keyUp", key: "Tab", code: "Tab", windowsVirtualKeyCode: 9, modifiers: shift ? 8 : 0 });
      await sleep(60);
      out.push(await cdp.evaluate(`(() => { const a = document.activeElement; const row = a.closest(".notes > .note");
        window.__tabIds ||= new WeakMap(); if (!__tabIds.has(a)) __tabIds.set(a, __tabIds.size ?? 0); window.__tabN = (window.__tabN || 0);
        if (!a.dataset.tabId) a.dataset.tabId = String(++window.__tabN);
        return (row ? row.dataset.id : "-") + ":" + (a.getAttribute("aria-label") || a.className || a.tagName) + "#" + a.dataset.tabId; })()`));
    }
    return out;
  };
  await cdp.evaluate(`document.querySelector(".notes > .note .drag-handle").focus()`);
  const fwd = await seq(false, 14);
  const back = await seq(true, 13);
  const unique = new Set(fwd).size === fwd.length;
  const mirrored = JSON.stringify(back) === JSON.stringify(fwd.slice(0, -1).reverse());
  const tabSel = await state();
  check("Tab and Shift+Tab visit each control in a note once, in order, without selecting", unique && mirrored && tabSel.selectedIndex === -1, { fwd, back, sel: tabSel.selectedIndex });
  return checks;
}

// Interactive states, checked with axe like the page states: selected row, open menus, toast,
// editor and shortcut list, in light and dark, desktop and phone.
async function statesAxe(cdp, WEB) {
  const out = [];
  const reached = { "selected row": ".note.selected", "move menu": ".move-menu:not(.note-menu)", "editor": ".edit-box", "toast": "#toast:not([hidden])",
    "shortcut list": "#keys-dialog[open]", "⋯ menu": ".note-menu" };
  const axeNow = async (label) => {
    const sel = reached[label.split(" · ")[0]];
    if (sel && !(await cdp.evaluate(`Boolean(document.querySelector(${JSON.stringify(sel)}))`))) {
      out.push({ label, violations: [{ rule: "state-not-reached", target: sel, detail: "the check could not reach this state" }], incomplete: [] });
      return;
    }
    const r = await cdp.evaluate(`(async () => {
      if (!window.axe) eval(${JSON.stringify(AXE)});
      const r = await axe.run(document, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa", "best-practice"] } });
      return { violations: r.violations.flatMap((v) => v.nodes.map((n) => ({ rule: v.id, target: n.target.join(" "), detail: (n.any[0] || n.all[0] || n.none[0] || {}).message || v.help }))),
               incomplete: r.incomplete.filter((v) => v.id === "color-contrast").flatMap((v) => v.nodes.map((n) => n.target.join(" ") + " :: " + ((n.any[0] || {}).message || "").slice(0, 120))) };
    })()`);
    out.push({ label, ...r });
  };
  const click = (sel) => cdp.evaluate(`document.querySelector(${JSON.stringify(sel)})?.click()`).then(() => sleep(250));
  for (const dark of [false, true]) {
    const theme = dark ? "dark" : "light";
    await setViewport(cdp, false, dark);
    await load(cdp, `${WEB}/?demo#/day`);
    await cdp.evaluate(`(document.activeElement || document.body).dispatchEvent(new KeyboardEvent("keydown", { key: "j", bubbles: true }))`); await sleep(150);
    await axeNow(`selected row · ${theme} · desktop`);
    await cdp.evaluate(`(document.activeElement || document.body).dispatchEvent(new KeyboardEvent("keydown", { key: "m", bubbles: true }))`); await sleep(250);
    await axeNow(`move menu · ${theme} · desktop`);
    await cdp.evaluate(`document.querySelector(".move-menu")?.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }))`); await sleep(150);
    await cdp.evaluate(`(document.activeElement || document.body).dispatchEvent(new KeyboardEvent("keydown", { key: "e", bubbles: true }))`); await sleep(250);
    await axeNow(`editor · ${theme} · desktop`);
    await cdp.evaluate(`document.querySelector(".edit-box")?.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }))`); await sleep(200);
    await click(".notes > .note .icon-btn.danger");
    await sleep(1200); // let the list finish re-rendering (rows fade in)
    await axeNow(`toast · ${theme} · desktop`);
    await cdp.evaluate(`document.querySelector("#keys-dialog")?.showModal()`); await sleep(150);
    await axeNow(`shortcut list · ${theme} · desktop`);
    await setViewport(cdp, true, dark);
    await load(cdp, `${WEB}/?demo#/day`);
    await click(".notes > .note:not(.done) .more-btn");
    await axeNow(`⋯ menu · ${theme} · phone`);
    await cdp.evaluate(`[...document.querySelectorAll(".note-menu .menu-item")].find((b) => /Move to another day/.test(b.textContent))?.click()`); await sleep(250);
    await axeNow(`move menu · ${theme} · phone`);
  }
  return out;
}

// Existing flows still behave as before (demo store). Each step checks the DOM after the action.
async function existingFlows(cdp, WEB) {
  const checks = [];
  const step = async (name, script) => {
    try {
      const r = await cdp.evaluate(`(async () => { const wait = (ms) => new Promise((r) => setTimeout(r, ms)); const $ = (s) => document.querySelector(s);
        // a real click needs a visible control that is on top at its centre
        const tap = async (el) => {
          if (typeof el === "string") el = document.querySelector(el);
          if (!el) throw new Error("control not found");
          const r0 = el.getBoundingClientRect();
          if (r0.top < 0 || r0.bottom > innerHeight) { el.scrollIntoView({ block: "center" }); await wait(200); } // let the scroll settle first
          const b = el.getBoundingClientRect(), cs = getComputedStyle(el);
          if (!b.width || !b.height || cs.visibility === "hidden" || cs.display === "none") throw new Error("control is hidden");
          const at = document.elementFromPoint(b.left + b.width / 2, b.top + b.height / 2);
          if (!(at === el || el.contains(at))) throw new Error("control is covered by " + (at?.className || at?.tagName));
          el.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true })); el.click();
        };
        ${script} })()`);
      checks.push({ name, ok: r === true, detail: r });
    } catch (e) { checks.push({ name, ok: false, detail: e.message.slice(0, 160) }); }
  };
  const fresh = async (phone = false) => { await setViewport(cdp, phone, false); await load(cdp, `${WEB}/?demo#/day`); };
  await fresh();
  await step("add a note from the composer", `
    const before = document.querySelectorAll(".notes > .note").length;
    const box = $("#composer-input"); box.value = "Smoke test note"; box.dispatchEvent(new Event("input"));
    $("#composer").requestSubmit(); await wait(300);
    return document.querySelectorAll(".notes > .note").length === before + 1 && [...document.querySelectorAll(".note-text")].some((t) => t.textContent === "Smoke test note");`);
  await step("tick a note by clicking", `
    const open = () => document.querySelectorAll(".notes > .note:not(.done)").length;
    const before = open(); await tap(".notes > .note:not(.done):not(:has(.checklist)) .check"); await wait(300);
    return open() === before - 1;`);
  await step("tick a checklist item", `
    const done = () => document.querySelectorAll(".cl-check[aria-checked=true]").length;
    const before = done(); await tap(".cl-check[aria-checked=false]"); await wait(300);
    return done() === before + 1;`);
  await step("add a checklist item", `
    await tap(".cl-add-btn"); await wait(150);
    const input = $(".cl-add input"); input.value = "Smoke item"; input.closest("form").requestSubmit(); await wait(300);
    return [...document.querySelectorAll(".checklist li span")].some((s) => s.textContent === "Smoke item");`);
  await step("reorder with the keyboard (↓ on the handle)", `
    const ids = () => [...document.querySelectorAll(".notes > .note")].map((n) => n.dataset.id);
    const before = ids(); const handle = $(".notes > .note .drag-handle"); handle.focus();
    handle.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true })); await wait(400);
    const after = ids(); return after[0] === before[1] && after[1] === before[0];`);
  await step("move a note to tomorrow with the Move menu", `
    const row = $(".notes > .note:not(.done)"); const id = row.dataset.id;
    await tap(row.querySelector(".icon-btn.go")); await wait(150);
    const tomorrow = [...document.querySelectorAll(".move-menu > button")].find((b) => b.textContent.startsWith("Tomorrow"));
    await tap(tomorrow); await wait(300);
    return !document.querySelector('.notes > .note[data-id="' + id + '"]');`);
  await step("share dialog adds a person", `
    await tap(".notes > .note .icon-btn.share"); await wait(150);
    const ok1 = $("#share-dialog").open; $("#share-email").value = "teammate@example.com";
    $("#share-form").requestSubmit(); await wait(300);
    const ok2 = $("#share-list").textContent.includes("teammate@example.com"); $("#share-close").click(); await wait(100);
    return ok1 && ok2 && !$("#share-dialog").open;`);
  await step("trash a note and undo", `
    const count = () => document.querySelectorAll(".notes > .note").length;
    const before = count(); await tap(".notes > .note .icon-btn.danger"); await wait(300);
    const gone = count() === before - 1; await tap("#toast-action"); await wait(300);
    return gone && count() === before;`);
  await step("search finds a note", `
    const s = $("#search"); s.value = "OKRs"; s.dispatchEvent(new Event("input", { bubbles: true })); await wait(300);
    const hits = [...document.querySelectorAll(".notes > .note .note-text")].map((t) => t.textContent);
    s.value = ""; s.dispatchEvent(new Event("input", { bubbles: true })); await wait(200);
    return hits.length >= 1 && hits.every((t) => /okrs/i.test(t));`);
  await step("theme switch to dark and back", `
    $('[data-theme-choice="dark"]').click(); await wait(100);
    const dark = getComputedStyle(document.body).backgroundColor;
    $('[data-theme-choice="light"]').click(); await wait(100);
    const light = getComputedStyle(document.body).backgroundColor;
    $('[data-theme-choice="system"]').click();
    return dark === "rgb(20, 19, 18)" && light === "rgb(247, 246, 243)";`);
  await load(cdp, `${WEB}/?demo#/week`);
  await step("week view shows notes and ticks them", `
    const cards = document.querySelectorAll(".week-card"); const open = () => document.querySelectorAll(".week-card:not(.done)").length;
    const before = open(); await tap(".week-card:not(.done) .check"); await wait(300);
    return cards.length > 0 && open() === before - 1;`);
  await fresh(true);
  await step("phone: the ⋯ menu offers the note's actions and opens Move", `
    await tap(".notes > .note:not(.done) .more-btn"); await wait(150);
    const labels = [...document.querySelectorAll(".note-menu .menu-item")].map((b) => b.textContent.trim());
    const want = ["Edit", "Move to another day…", "Share", "Move to Trash"].every((l) => labels.includes(l));
    [...document.querySelectorAll(".note-menu .menu-item")].find((b) => b.textContent.includes("Move to another day")).click(); await wait(150);
    const moveOpen = Boolean(document.querySelector(".move-menu:not(.note-menu)"));
    return want && moveOpen ? true : { labels, moveOpen };`);
  await load(cdp, `${WEB}/?demo#/trash`);
  await step("phone: Trash rows keep Restore and Delete forever", `
    const row = $(".notes > .note"); if (!row) return "no trashed note";
    const restore = [...row.querySelectorAll("button")].find((b) => /Restore/.test(b.textContent));
    const forever = row.querySelector('[aria-label="Delete forever"]');
    const before = document.querySelectorAll(".notes > .note").length;
    const foreverOk = Boolean(forever) && forever.getBoundingClientRect().width >= 24;
    await tap(restore); await wait(300);
    return foreverOk && document.querySelectorAll(".notes > .note").length === before - 1 ? true : { foreverOk, before, after: document.querySelectorAll(".notes > .note").length };`);
  return checks;
}

function report(results, keyboard, flows, states) {
  const failures = [];
  const contrastFails = (r) => r.violations.filter((v) => v.rule === "color-contrast");
  const totalAxe = results.reduce((s, r) => s + r.violations.length, 0);
  const phone = results.filter((r) => r.phone);
  const small = phone.flatMap((r) => r.targets.filter((t) => t.w < TARGETS.minTarget || t.h < TARGETS.minTarget).map((t) => ({ ...t, where: r.label })));
  const smallPrimary = phone.flatMap((r) => r.targets.filter((t) => t.primary && (t.w < TARGETS.primaryTarget || t.h < TARGETS.primaryTarget)).map((t) => ({ ...t, where: r.label })));
  const phoneControls = phone.reduce((s, r) => s + r.targets.length, 0);
  const covered = phone.flatMap((r) => r.targets.filter((t) => t.reach < 95).map((t) => ({ ...t, where: r.label })));
  const todayPhone = results.filter((r) => r.page === "day" && r.phone);
  const fold = Math.min(...todayPhone.map((r) => r.aboveFold));
  const stateAxe = states.reduce((s, r) => s + r.violations.length, 0);
  const incomplete = [...results, ...states].flatMap((r) => (r.incomplete || []).map((i) => `${r.label}: ${i}`));
  const overflow = results.filter((r) => r.overflowX).map((r) => r.label);

  console.log("QuickNotes UI check");
  console.log(`  pages measured: ${results.length} (7 views + popup, light/dark, desktop/phone)`);
  console.log(`  axe violations: ${totalAxe} (contrast ${results.reduce((s, r) => s + contrastFails(r).length, 0)}) — target ${TARGETS.axeViolations}`);
  for (const r of results.filter((r) => r.violations.length)) {
    const byRule = Object.entries(r.violations.reduce((m, v) => ((m[v.rule] = (m[v.rule] || 0) + 1), m), {})).map(([k, v]) => `${k}×${v}`).join(", ");
    console.log(`    ${r.label}: ${byRule}`);
  }
  console.log(`  phone controls under ${TARGETS.minTarget}px: ${small.length} of ${phoneControls}`);
  for (const t of dedupe(small).slice(0, 8)) console.log(`    ${t.w}x${t.h} "${t.name}" (${t.where})`);
  console.log(`  phone primary actions under ${TARGETS.primaryTarget}pt: ${smallPrimary.length}`);
  for (const t of dedupe(smallPrimary).slice(0, 8)) console.log(`    ${t.w}x${t.h} "${t.name}" (${t.where})`);
  console.log(`  phone controls partly covered by another element (tap area < 95% reachable): ${covered.length}`);
  for (const t of dedupe(covered).slice(0, 8)) console.log(`    ${t.reach}% "${t.name}" (${t.where})`);
  console.log(`  interactive states (selected row, menus, toast, editor, shortcut list): ${states.length} checked, axe violations ${stateAxe}`);
  for (const r of states.filter((r) => r.violations.length)) console.log(`    ${r.label}: ${r.violations.map((v) => `${v.rule} ${v.target}`).slice(0, 4).join("; ")}`);
  console.log(`  contrast axe could not decide (needs a human look): ${incomplete.length}`);
  for (const i of [...new Set(incomplete)].slice(0, 8)) console.log(`    ${i}`);
  console.log(`  notes above the fold (Today, phone): ${fold} — target ≥ ${TARGETS.notesAboveFold}`);
  console.log(`  horizontal overflow: ${overflow.length ? overflow.join("; ") : "none"}`);
  console.log(`  keyboard triage: ${keyboard.filter((c) => c.ok).length}/${keyboard.length}`);
  for (const c of keyboard) console.log(`    ${c.ok ? "✓" : "✗"} ${c.name}${c.ok ? "" : "  " + JSON.stringify(c.detail)}`);
  console.log(`  existing flows: ${flows.filter((c) => c.ok).length}/${flows.length}`);
  for (const c of flows) console.log(`    ${c.ok ? "✓" : "✗"} ${c.name}${c.ok ? "" : "  " + JSON.stringify(c.detail)}`);

  if (totalAxe > TARGETS.axeViolations) failures.push("axe violations");
  if (small.length) failures.push("small targets");
  if (covered.length) failures.push("covered tap areas");
  if (stateAxe) failures.push("axe violations in interactive states");
  if (smallPrimary.length) failures.push("small primary actions");
  if (fold < TARGETS.notesAboveFold) failures.push("notes above the fold");
  if (overflow.length) failures.push("horizontal overflow");
  if (keyboard.some((c) => !c.ok)) failures.push("keyboard triage");
  if (flows.some((c) => !c.ok)) failures.push("existing flows");

  const summary = { axeViolations: totalAxe, contrastViolations: results.reduce((s, r) => s + contrastFails(r).length, 0), phoneControls,
    phoneUnder24: small.length, phonePrimaryUnder44: smallPrimary.length, coveredTapAreas: covered.length, stateAxeViolations: stateAxe,
    contrastUndecided: incomplete.length, notesAboveFold: fold, overflow, keyboard, flows, failures };
  if (opt("--json")) writeFileSync(opt("--json"), JSON.stringify({ summary, results, states }, null, 2));
  console.log(failures.length ? `RESULT: below the bar (${failures.join(", ")})` : "RESULT: meets the bar");
  process.exitCode = failures.length ? 1 : 0;
}

function dedupe(list) {
  const seen = new Set();
  return list.filter((t) => { const k = `${t.name}|${t.w}x${t.h}`; if (seen.has(k)) return false; seen.add(k); return true; });
}

main().catch((e) => { console.error(e.message.startsWith("blocked") ? e.message : `error: ${e.stack}`); process.exit(2); });
