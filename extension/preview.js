// Sample data for looking at popup.html in a normal browser tab (not used inside Chrome's extension).
import { CONFIG as LIVE } from "./config.js";
export const CONFIG = { webURL: LIVE.webURL };
export const today = () => new Date().toISOString().slice(0, 10);

let session = new URLSearchParams(location.search).has("signedout")
  ? null
  : { uid: "me", email: "you@example.com", name: "You" };
let id = 0;
const note = (content, extra = {}) => ({ id: `p${++id}`, ownerId: "me", content, status: "open", ...extra });
const notes = [
  note("Book room for Thursday retro"),
  note("Reply to customer escalation #4471"),
  note("Vendor contract: legal to review indemnity clause", { ownerId: "priya", ownerName: "Priya Nair" }),
  note("Standup: API migration blocked on auth review", { status: "done" }),
];

export const getSession = async () => session;
export const signIn = async () => { session = { uid: "me", email: "you@example.com", name: "You" }; return session; };
export const signOut = async () => { session = null; };
export const notesOn = async () => notes.map((n) => ({ ...n }));
export const addNote = async (content) => { notes.splice(notes.findIndex((n) => n.status === "done"), 0, note(content)); };
export const setStatus = async (n, status) => { notes.find((x) => x.id === n.id).status = status; };
