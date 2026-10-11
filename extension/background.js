// Right-click "Save to QuickNotes", the open-count badge on the toolbar icon,
// and sign-in (run here because the popup closes when the sign-in window opens).

import { addNote, notesOn, NotSignedIn, recordUser, signIn, today } from "./cloud.js";

const ACCENT = "#E0612A";

chrome.runtime.onInstalled.addListener(() => {
  chrome.contextMenus.create({ id: "save-selection", title: "Save “%s” to QuickNotes", contexts: ["selection"] });
  chrome.contextMenus.create({ id: "save-link", title: "Save link to QuickNotes", contexts: ["link"] });
  chrome.contextMenus.create({ id: "save-page", title: "Save this page to QuickNotes", contexts: ["page"] });
  chrome.alarms.create("badge", { periodInMinutes: 15 });
  updateBadge();
});

chrome.runtime.onStartup.addListener(updateBadge);
chrome.alarms.onAlarm.addListener((alarm) => { if (alarm.name === "badge") updateBadge(); });

chrome.contextMenus.onClicked.addListener(async (info, tab) => {
  const source = tab?.url ? `\n— ${tab.title || tab.url} (${tab.url})` : "";
  let text;
  if (info.menuItemId === "save-selection") text = `“${info.selectionText.trim()}”${source}`;
  else if (info.menuItemId === "save-link") text = `${info.linkUrl}${source}`;
  else text = `${tab?.title || "Page"}\n${tab?.url || ""}`;

  try {
    await addNote(text.trim());
    flash("✓", "#2F9E5B", "Saved to today");
  } catch (e) {
    flash("!", "#C93A31", e instanceof NotSignedIn ? "Sign in first: click the QuickNotes icon" : `Couldn’t save: ${e.message}`);
  }
});

chrome.runtime.onMessage.addListener((message, _sender, reply) => {
  if (message?.type === "signIn") {
    signIn().then(() => {
      updateBadge();
      recordUser({ force: true }).catch((e) => console.warn("Couldn’t save user profile", e));
      reply({ ok: true });
    }, (e) => reply({ error: e.message }));
    return true; // reply asynchronously
  }
  if (message?.type === "refreshBadge") updateBadge();
  return false;
});

/** Shows a short-lived badge (e.g. ✓ after saving), then goes back to the open count. */
function flash(text, color, title) {
  chrome.action.setBadgeText({ text });
  chrome.action.setBadgeBackgroundColor({ color });
  chrome.action.setTitle({ title: `QuickNotes: ${title}` });
  setTimeout(() => {
    chrome.action.setTitle({ title: "QuickNotes (⌥⇧N)" });
    updateBadge();
  }, 2500);
}

async function updateBadge() {
  try {
    const open = (await notesOn(today())).filter((n) => n.status === "open").length;
    chrome.action.setBadgeText({ text: open ? String(open) : "" });
    chrome.action.setBadgeBackgroundColor({ color: ACCENT });
  } catch {
    chrome.action.setBadgeText({ text: "" });
  }
}
