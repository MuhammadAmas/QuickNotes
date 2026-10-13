// Toolbar popup: add a note for today, tick off today's notes.
// Opened outside Chrome's extension pages (e.g. for a design check) it uses sample data instead.

const inExtension = Boolean(globalThis.chrome?.storage);
const cloud = await import(inExtension ? "./cloud.js" : "./preview.js");
const { CONFIG, addNote, getSession, notesOn, setStatus, signOut, today } = cloud;

const $ = (id) => document.getElementById(id);
const show = (id) => { for (const s of ["loading", "signed-out", "signed-in"]) $(s).hidden = s !== id; };

$("date").textContent = new Date().toLocaleDateString(undefined, { weekday: "long", month: "short", day: "numeric" });
$("open-app").addEventListener("click", (e) => {
  e.preventDefault();
  if (inExtension) chrome.tabs.create({ url: CONFIG.webURL }); else window.open(CONFIG.webURL);
  window.close();
});

// ---------- links in note text ----------

// web addresses with or without https:// or www. (bare ones only with a known ending like .com/.ai,
// and not inside email addresses or file names)
const TLDS = "com|org|net|io|ai|co|dev|app|me|so|gg|xyz|info|biz|tech|cloud|site|online|page|link|ly|to|tv|fm|design|studio|team|tools|digital|agency|store|shop|blog|news|edu|gov|mil|int|us|uk|in|pk|ca|de|fr|au|nl|es|it|jp|cn|ru|br|ch|se|no|dk|fi|ie|nz|za|sg|ae|sa|tr|pl|be|at|il|kr|mx|ar|cl|cc|ws|la|eu|asia";
const URL_RE = new RegExp(
  String.raw`(?<![@\w.\-/])(?:https?:\/\/[^\s<>"']+|www\.[^\s<>"']+|(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+(?:${TLDS})(?![\w-])(?:[/?#][^\s<>"']*)?)`,
  "gi");

/** Fills `el` with the note text, web addresses as links that open in a new tab. */
function setLinkedText(el, text) {
  el.replaceChildren();
  let last = 0;
  for (const match of text.matchAll(URL_RE)) {
    let url = match[0].replace(/[.,!?;:'"]+$/, "");
    while (/[)\]}]$/.test(url) && (url.match(/[(\[{]/g) || []).length < (url.match(/[)\]}]/g) || []).length) url = url.slice(0, -1);
    let href;
    try { href = new URL(/^https?:\/\//i.test(url) ? url : `https://${url}`); } catch { continue; }
    if (href.protocol !== "http:" && href.protocol !== "https:") continue;

    el.append(text.slice(last, match.index));
    const a = document.createElement("a");
    const shown = url.replace(/^https?:\/\//, "").replace(/\/$/, "");
    a.textContent = shown.length > 48 ? `${shown.slice(0, 45)}…` : shown;
    a.href = href.href;
    a.title = href.href;
    a.className = "link";
    a.addEventListener("click", (e) => {
      e.preventDefault();
      if (inExtension) chrome.tabs.create({ url: href.href }); else window.open(href.href, "_blank", "noopener");
      window.close();
    });
    el.append(a);
    last = match.index + url.length;
  }
  el.append(text.slice(last));
}

let notes = [];
let shot = null; // { full, thumb, width, height } once "Screenshot this tab" is used

// ---------- screenshot of the current tab ----------

const loadImage = (src) => new Promise((resolve, reject) => {
  const img = new Image();
  img.onload = () => resolve(img);
  img.onerror = () => reject(new Error("Couldn’t read the screenshot"));
  img.src = src;
});

/** Scales to `maxSide` and re-encodes as JPEG. */
function jpeg(img, maxSide, quality) {
  const scale = Math.min(1, maxSide / Math.max(img.naturalWidth, img.naturalHeight));
  const canvas = document.createElement("canvas");
  canvas.width = Math.round(img.naturalWidth * scale);
  canvas.height = Math.round(img.naturalHeight * scale);
  canvas.getContext("2d").drawImage(img, 0, 0, canvas.width, canvas.height);
  return { url: canvas.toDataURL("image/jpeg", quality), width: canvas.width, height: canvas.height };
}

async function captureTab() {
  const raw = await chrome.tabs.captureVisibleTab(undefined, { format: "png" });
  const img = await loadImage(raw);
  // small enough to fit a Firestore document once base64-encoded
  let full = null;
  for (const side of [2000, 1600, 1280, 1024]) {
    for (const q of [0.8, 0.68, 0.55]) {
      const out = jpeg(img, side, q);
      if (out.url.length < 900_000) { full = out; break; }
    }
    if (full) break;
  }
  if (!full) throw new Error("This page is too large to capture");
  return { full: full.url, width: full.width, height: full.height, thumb: jpeg(img, 480, 0.55).url };
}

function setShot(value) {
  shot = value;
  $("shot-preview").hidden = !shot;
  $("shot-img").src = shot ? shot.thumb : "";
  $("input").placeholder = shot ? "Add a description (optional)" : "What’s on your mind?";
}

$("shot-btn").addEventListener("click", async () => {
  if (!inExtension) return setStatusText("Screenshots work inside Chrome only", "error");
  $("shot-btn").disabled = true;
  try {
    setShot(await captureTab());
    setStatusText("");
  } catch (e) {
    // Chrome won't capture its own pages (chrome://, the Web Store, PDFs)
    setStatusText(/Cannot access|cannot be scripted|permission/i.test(e.message) ? "Chrome doesn’t allow screenshots of this page" : e.message, "error");
  } finally {
    $("shot-btn").disabled = false;
    $("input").focus();
  }
});
$("shot-remove").addEventListener("click", () => { setShot(null); $("input").focus(); });

function setStatusText(text, kind = "") {
  $("status").textContent = text;
  $("status").className = `status ${kind}`;
}

function renderNotes() {
  const list = $("notes");
  list.replaceChildren();
  const open = notes.filter((n) => n.status === "open");
  $("count").textContent = notes.length ? `${notes.length - open.length} of ${notes.length} done` : "";
  if (!notes.length) {
    const li = document.createElement("li");
    li.className = "empty";
    li.textContent = "Nothing for today yet.";
    list.append(li);
    return;
  }
  for (const n of [...open, ...notes.filter((x) => x.status === "done")]) {
    const done = n.status === "done";
    const li = document.createElement("li");
    li.className = `note${done ? " done" : ""}`;

    const check = document.createElement("button");
    check.className = "check";
    check.setAttribute("role", "checkbox");
    check.setAttribute("aria-checked", String(done));
    check.setAttribute("aria-label", done ? "Mark as not done" : "Mark as done");
    check.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 6 9 17l-5-5"/></svg>';
    check.addEventListener("click", () => toggle(n));

    const text = document.createElement("span");
    text.className = "text";
    setLinkedText(text, n.content);
    const items = n.checklist || [];
    if (items.length) {
      const progress = document.createElement("span");
      progress.className = "progress";
      progress.textContent = ` ☑ ${items.filter((i) => i.done).length}/${items.length}`;
      text.append(progress);
    }
    if (n.ownerName && n.ownerId !== currentUid) text.title = `From ${n.ownerName}`;

    li.append(check, text);
    list.append(li);
  }
}

let currentUid = null;

async function loadNotes() {
  try {
    notes = await notesOn(today());
    renderNotes();
  } catch (e) {
    if (e.name === "NotSignedIn" || e.message === "Not signed in") return start();
    setStatusText(e.message, "error");
  }
  if (inExtension) chrome.runtime.sendMessage({ type: "refreshBadge" });
}

async function toggle(note) {
  const status = note.status === "done" ? "open" : "done";
  note.status = status; // optimistic
  renderNotes();
  try { await setStatus(note, status); } catch (e) { setStatusText(e.message, "error"); }
  loadNotes();
}

async function start() {
  const session = await getSession();
  if (!session) { show("signed-out"); return; }
  currentUid = session.uid;
  $("account").textContent = session.email;
  show("signed-in");
  $("input").focus();
  loadNotes();
  cloud.recordUser?.()?.catch((e) => console.warn("Couldn’t save user profile", e));
}

// ---------- composer ----------

const input = $("input");
const grow = () => { input.style.height = "auto"; input.style.height = `${Math.min(input.scrollHeight, 140)}px`; };
input.addEventListener("input", () => { grow(); if ($("status").classList.contains("ok")) setStatusText(""); });
input.addEventListener("keydown", (e) => {
  if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); $("composer").requestSubmit(); }
  if (e.key === "Escape") window.close();
});
$("composer").addEventListener("submit", async (e) => {
  e.preventDefault();
  const text = input.value.trim();
  if (!text && !shot) return;
  setStatusText("Saving…");
  input.disabled = true;
  try {
    await addNote(text, today(), shot);
    input.value = "";
    setShot(null);
    grow();
    setStatusText("✓ Saved to today", "ok");
    loadNotes();
  } catch (err) {
    setStatusText(`Couldn’t save: ${err.message}`, "error");
  } finally {
    input.disabled = false;
    input.focus();
  }
});

// ---------- account ----------

$("sign-in").addEventListener("click", async () => {
  $("auth-error").hidden = true;
  $("sign-in").disabled = true;
  // runs in the background worker: this popup closes when Chrome's sign-in window takes focus
  const result = inExtension ? await chrome.runtime.sendMessage({ type: "signIn" }) : await cloud.signIn();
  $("sign-in").disabled = false;
  if (result?.error) {
    $("auth-error").textContent = result.error;
    $("auth-error").hidden = false;
  } else {
    start();
  }
});
$("sign-out").addEventListener("click", async () => {
  await signOut();
  if (inExtension) chrome.action.setBadgeText({ text: "" });
  start();
});

start();
