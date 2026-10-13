// In-memory store for trying the UI locally without signing in:
// http://localhost:<port>/?demo  (only enabled on localhost).

import { newNote, patchFor, nowIso } from "./notes-model.js";

const pad = (n) => String(n).padStart(2, "0");
const day = (offset) => {
  const d = new Date();
  d.setDate(d.getDate() + offset);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
};

// a stand-in screenshot for the demo
const DEMO_SHOT = "data:image/svg+xml;base64," + btoa(`<svg xmlns="http://www.w3.org/2000/svg" width="800" height="500" viewBox="0 0 800 500">
  <rect width="800" height="500" fill="#f4f1ec"/><rect x="0" y="0" width="800" height="56" fill="#1f2023"/>
  <rect x="60" y="110" width="320" height="40" rx="8" fill="#d9d4ca"/><rect x="60" y="170" width="520" height="18" rx="9" fill="#e6e1d8"/>
  <rect x="60" y="250" width="200" height="190" rx="14" fill="#fff" stroke="#e0612a" stroke-width="4"/>
  <rect x="290" y="250" width="200" height="190" rx="14" fill="#fff"/><rect x="520" y="250" width="200" height="190" rx="14" fill="#fff"/>
  <rect x="210" y="226" width="140" height="44" rx="22" fill="#e0612a"/></svg>`);

const me = { uid: "demo-me", email: "you@example.com", name: "You", photo: "" };
const priya = { uid: "demo-priya", email: "priya@example.com", name: "Priya Nair", photo: "" };

function seed() {
  let id = 0;
  const make = (owner, content, date, extra = {}) => ({ id: `n${++id}`, ...newNote(owner, content, date), order: id * 1000, ...extra });
  return [
    make(me, "Follow up with design team on onboarding flow", day(-2)),
    make(me, "Send Q4 budget draft to finance", day(-1), { sharedWith: ["priya@example.com"] }),
    make(me, "Standup: API migration blocked on auth review", day(0), { status: "done", completed_at: nowIso() }),
    make(me, "Book room for Thursday retro", day(0)),
    make(me, "Read Sam’s launch checklist before Friday: https://docs.example.com/launch/checklist-q4 (and www.example.org/faq).", day(0)),
    make(me, "Reply to customer escalation #4471", day(0), { edited_at: nowIso() }),
    make(priya, "Vendor contract: legal to review indemnity clause", day(0), { sharedWith: [me.email] }),
    make(me, "Prep slides for Monday review", day(1), { times_moved: 1, original_date: day(0) }),
    make(me, "Pricing page: the annual toggle overlaps the plan cards on small screens", day(0), {
      attachment: { id: "demo-shot", kind: "screenshot", width: 1600, height: 1000, thumb: DEMO_SHOT },
    }),
  ];
}

export function createStore() {
  let user = null;
  let notes = seed();
  let authCb = () => {};
  const listeners = new Set();
  const emit = () => queueMicrotask(() => {
    const visible = notes.filter((n) => n.ownerId === user?.uid || n.sharedWith.includes(user?.email));
    listeners.forEach((fn) => fn(visible.map((n) => ({ ...n }))));
  });
  const find = (id) => notes.find((n) => n.id === id);

  return {
    onAuth(callback) {
      authCb = callback;
      user = me;
      queueMicrotask(() => callback(user));
      return () => {};
    },
    async signIn() { user = me; authCb(user); },
    async signOut() { user = null; authCb(null); },
    subscribe(onNotes) {
      listeners.add(onNotes);
      emit();
      return () => listeners.delete(onNotes);
    },
    async create(content, noteDate) {
      notes.push({ id: `n${Date.now()}`, ...newNote(user, content, noteDate) });
      emit();
    },
    async update(note, changes) {
      Object.assign(find(note.id), patchFor(find(note.id), changes));
      emit();
    },
    async updateMany(list, changes) {
      for (const n of list) Object.assign(find(n.id), patchFor(find(n.id), changes));
      emit();
    },
    async setOrders(pairs) {
      for (const [n, order] of pairs) Object.assign(find(n.id), patchFor(find(n.id), { order }));
      emit();
    },
    async remove(note) {
      notes = notes.filter((n) => n.id !== note.id);
      emit();
    },
    async getAttachment() {
      return DEMO_SHOT;
    },
    async setSharing(note, emails) {
      Object.assign(find(note.id), { sharedWith: emails, updated_at: nowIso() });
      emit();
    },
  };
}
