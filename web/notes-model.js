// Shape of a note document and the field changes for each edit.
// Shared by the Firestore store and the demo store so both behave the same.

export const nowIso = () => new Date().toISOString();
export const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
export const MAX_CONTENT = 10000;
export const MAX_SHARES = 50;
export const MAX_CHECKLIST = 100;
export const TRASH_DAYS = 30;

const itemId = () => Math.random().toString(36).slice(2, 10);
export const checklistItem = (text, done = false) => ({ id: itemId(), text: text.slice(0, 500), done });

// "- milk", "* milk", "• milk", "[ ] milk", "[x] milk", "- [x] milk"
const LIST_LINE = /^\s*(?:[-*•]\s*)?\[\s*([xX✓]?)\s*\]\s+(.+)$|^\s*[-*•]\s+(.+)$/;

/**
 * Splits typed text into a title and checklist items. Lines that look like list items become the
 * checklist; the other lines are the note's text. Returns { content, checklist } (checklist may be empty).
 */
export function parseChecklist(text) {
  const lines = text.split("\n");
  const items = [];
  const rest = [];
  for (const line of lines) {
    const m = line.match(LIST_LINE);
    if (m) items.push(checklistItem((m[2] || m[3]).trim(), Boolean(m[1])));
    else if (line.trim()) rest.push(line.trim());
  }
  if (!items.length) return { content: text.trim(), checklist: [] };
  return { content: rest.join("\n") || "Checklist", checklist: items.slice(0, MAX_CHECKLIST) };
}

export function newNote(user, content, note_date, checklist = []) {
  const ts = nowIso();
  return {
    ownerId: user.uid,
    ownerEmail: user.email,
    ownerName: user.name,
    content,
    note_date,
    status: "open",
    created_at: ts,
    updated_at: ts,
    edited_at: null,
    completed_at: null,
    original_date: null,
    times_moved: 0,
    sharedWith: [],
    order: Date.now(), // new notes go to the bottom; dragging changes this
    ...(checklist.length ? { checklist } : {}),
  };
}

/** Fields to write for `changes` ({content?, status?, note_date?, order?, checklist?, deleted?}) applied to `note`. */
export function patchFor(note, changes) {
  const ts = nowIso();
  const patch = { updated_at: ts };
  if ("content" in changes && changes.content !== note.content) {
    patch.content = changes.content;
    patch.edited_at = ts;
  }
  if ("status" in changes && changes.status !== note.status) {
    patch.status = changes.status;
    patch.completed_at = changes.status === "done" ? ts : null;
  }
  if ("order" in changes) patch.order = changes.order;
  if ("checklist" in changes) patch.checklist = changes.checklist.slice(0, MAX_CHECKLIST);
  if ("deleted" in changes) patch.deleted_at = changes.deleted ? ts : null; // Trash / restore
  if ("note_date" in changes && changes.note_date !== note.note_date) {
    patch.note_date = changes.note_date;
    patch.original_date = note.original_date || note.note_date;
    patch.times_moved = (note.times_moved || 0) + 1;
  }
  return patch;
}

export function normalizeEmails(list) {
  const clean = list.map((e) => String(e).trim().toLowerCase()).filter((e) => EMAIL_RE.test(e));
  return [...new Set(clean)].slice(0, MAX_SHARES);
}
