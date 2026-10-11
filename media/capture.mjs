// Captures screenshots of the web app (demo data) and the extension popup for the demo video.
// Drives a throwaway headless Chrome profile over the DevTools protocol.
//
//   (cd web && python3 -m http.server 5599) & (cd extension && python3 -m http.server 5598) &
//   node media/capture.mjs <out-dir>

import { spawn } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const OUT = process.argv[2] || "media/shots";
const WEB = "http://localhost:5599/?demo";
const POPUP = "http://localhost:5598/popup.html";
const CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
mkdirSync(OUT, { recursive: true });

const profile = mkdtempSync(join(tmpdir(), "qn-capture-"));
const chrome = spawn(CHROME, [
  "--headless=new", "--remote-debugging-port=9333", `--user-data-dir=${profile}`,
  "--hide-scrollbars", "--no-first-run", "--no-default-browser-check", "--mute-audio",
], { stdio: "ignore" });

let ws, nextId = 0;
const pending = new Map();
function send(method, params = {}) {
  const id = ++nextId;
  ws.send(JSON.stringify({ id, method, params }));
  return new Promise((resolve, reject) => pending.set(id, { resolve, reject, method }));
}
const evaluate = async (expression) => {
  const r = await send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
  if (r.exceptionDetails) throw new Error(`${expression}\n${JSON.stringify(r.exceptionDetails)}`);
  return r.result.value;
};
async function shot(name) {
  await sleep(450); // let transitions settle
  const { data } = await send("Page.captureScreenshot", { format: "png" });
  writeFileSync(join(OUT, `${name}.png`), Buffer.from(data, "base64"));
  console.log("captured", name);
}
async function viewport(width, height, scale, mobile = false, dark = false) {
  await send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: scale, mobile });
  await send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: dark ? "dark" : "light" }] });
}
async function open(url) {
  await send("Page.navigate", { url: "about:blank" }); // a hash-only change wouldn't reload the page
  await sleep(200);
  await send("Page.navigate", { url });
  await sleep(1500);
}
const mouse = (type, x, y, buttons = 1) =>
  send("Input.dispatchMouseEvent", { type, x, y, button: "left", buttons, clickCount: 1, pointerType: "mouse" });
const rectOf = (js) => evaluate(`(() => { const r = (${js}).getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 }; })()`);
const note = (text) => `[...document.querySelectorAll("#list li.note")].find((li) => li.textContent.includes(${JSON.stringify(text)}))`;

try {
  for (let i = 0; i < 50; i++) {
    try { await fetch("http://127.0.0.1:9333/json/version"); break; } catch { await sleep(200); }
  }
  const target = await (await fetch("http://127.0.0.1:9333/json/new?about:blank", { method: "PUT" })).json();
  ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((r) => ws.addEventListener("open", r));
  ws.addEventListener("message", (e) => {
    const msg = JSON.parse(e.data);
    const p = pending.get(msg.id);
    if (!p) return;
    pending.delete(msg.id);
    msg.error ? p.reject(new Error(`${p.method}: ${msg.error.message}`)) : p.resolve(msg.result);
  });
  await send("Page.enable");
  await send("Runtime.enable");

  // ---- desktop web app, light ----
  await viewport(1440, 900, 2);
  await open(`${WEB}#/day`);
  await shot("web-today-before");

  // the note typed into the Mac bubble shows up in today's list
  await evaluate(`(() => { const i = document.getElementById("composer-input"); i.value = "Follow up with Sara on the pricing deck"; document.getElementById("composer").requestSubmit(); })()`);
  await shot("web-today");

  // tick one off
  await evaluate(`${note("Book room")}.querySelector(".check").click()`);
  await shot("web-ticked");

  // drag the new note to the top (real pointer events on its grip)
  const from = await rectOf(`${note("Follow up with Sara")}.querySelector(".drag-handle")`);
  const to = await rectOf(`document.querySelector("#list ul.notes li.note")`);
  await mouse("mouseMoved", from.x, from.y, 0);
  await mouse("mousePressed", from.x, from.y);
  for (let k = 1; k <= 12; k++) {
    await mouse("mouseMoved", from.x, from.y + ((to.y - 20 - from.y) * k) / 12);
    await sleep(30);
  }
  await shot("web-dragging");
  await mouse("mouseReleased", from.x, to.y - 20, 0);
  await shot("web-dragged");

  // everything still open, with overdue days
  await open(`${WEB}#/open`);
  await shot("web-open");

  // share a note
  await open(`${WEB}#/day`);
  await evaluate(`${note("Reply to customer")}.querySelector(".icon-btn.share").click()`);
  await evaluate(`(() => { document.getElementById("share-email").value = "sam@yourteam.com"; document.getElementById("share-form").requestSubmit(); })()`);
  await sleep(300);
  await evaluate(`(() => { document.getElementById("share-email").blur(); document.getElementById("toast").hidden = true; })()`);
  await shot("web-share");

  // ---- phone, dark ----
  await viewport(390, 844, 3, true, true);
  await open(`${WEB}#/day`);
  await shot("phone-dark");

  // ---- Chrome extension popup ----
  await viewport(360, 470, 2);
  await open(POPUP);
  await evaluate(`(() => { const i = document.getElementById("input"); i.value = "Check the onboarding email copy"; i.dispatchEvent(new Event("input")); i.blur(); })()`);
  await shot("popup");
} finally {
  ws?.close();
  chrome.kill();
  await sleep(300);
  rmSync(profile, { recursive: true, force: true });
}
