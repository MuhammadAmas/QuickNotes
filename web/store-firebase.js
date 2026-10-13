// Notes stored in Cloud Firestore, signed in with Google. Live updates come
// from two listeners: notes I own, and notes other people shared with me.

import { initializeApp } from "https://www.gstatic.com/firebasejs/10.14.1/firebase-app.js";
import {
  getAuth, GoogleAuthProvider, getRedirectResult, onAuthStateChanged,
  signInWithPopup, signInWithRedirect, signOut,
} from "https://www.gstatic.com/firebasejs/10.14.1/firebase-auth.js";
import {
  collection, deleteDoc, doc, getDoc, getDocs, increment, limit, initializeFirestore, memoryLocalCache, onSnapshot,
  persistentLocalCache, persistentMultipleTabManager, query, serverTimestamp, setDoc, updateDoc, where, writeBatch,
} from "https://www.gstatic.com/firebasejs/10.14.1/firebase-firestore.js";
import { firebaseConfig, NOTES_COLLECTION, USERS_COLLECTION } from "./firebase-config.js";
import { newNote, nowIso, patchFor } from "./notes-model.js";

export const app = initializeApp(firebaseConfig);
export const auth = getAuth(app);

let db;
function firestore() {
  if (!db) {
    try {
      // keeps a copy on the device: works offline and syncs when back online
      db = initializeFirestore(app, { localCache: persistentLocalCache({ tabManager: persistentMultipleTabManager() }) });
    } catch {
      db = initializeFirestore(app, { localCache: memoryLocalCache() });
    }
  }
  return db;
}

const noteRef = (id) => doc(firestore(), NOTES_COLLECTION, id);

export async function signInWithGoogle() {
  const provider = new GoogleAuthProvider();
  provider.setCustomParameters({ prompt: "select_account" });
  try {
    await signInWithPopup(auth, provider);
  } catch (e) {
    if (e.code === "auth/popup-blocked" || e.code === "auth/operation-not-supported-in-this-environment") {
      await signInWithRedirect(auth, provider);
    } else if (e.code !== "auth/popup-closed-by-user" && e.code !== "auth/cancelled-popup-request") {
      throw e;
    }
  }
}

/** Saves who is using the app to users/{uid} (merged, so earlier fields and other apps' entries stay). */
function recordUser(u) {
  const installed = matchMedia("(display-mode: standalone)").matches || navigator.standalone === true;
  return setDoc(doc(firestore(), USERS_COLLECTION, u.uid), {
    uid: u.uid,
    email: (u.email || "").toLowerCase(),
    displayName: u.displayName || "",
    photoURL: u.photoURL || "",
    providers: u.providerData.map((p) => p.providerId),
    createdAt: new Date(u.metadata.creationTime),
    lastSignInAt: new Date(u.metadata.lastSignInTime),
    lastSeenAt: serverTimestamp(),
    visits: increment(1),
    locale: navigator.language || "",
    timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone || "",
    clients: {
      web: {
        lastSeenAt: serverTimestamp(),
        installed, // added to the home screen / dock
        mobile: matchMedia("(max-width: 760px)").matches,
        userAgent: navigator.userAgent.slice(0, 300),
      },
    },
  }, { merge: true });
}

const toUser = (u) => u && {
  uid: u.uid,
  email: (u.email || "").toLowerCase(),
  name: u.displayName || u.email || "You",
  photo: u.photoURL || "",
};

export function createStore() {
  let user = null;

  return {
    onAuth(callback) {
      getRedirectResult(auth).catch((e) => callback(undefined, e));
      return onAuthStateChanged(auth, (u) => {
        user = toUser(u);
        if (u) recordUser(u).catch((e) => console.warn("Couldn’t save user profile", e));
        callback(user);
      });
    },

    signIn: signInWithGoogle,
    signOut: () => signOut(auth),

    subscribe(onNotes, onError) {
      const own = new Map();
      const shared = new Map();
      const emit = () => onNotes([...new Map([...shared, ...own]).values()]);
      const fill = (map) => (snap) => {
        map.clear();
        snap.forEach((d) => map.set(d.id, { id: d.id, ...d.data() }));
        emit();
      };
      const notes = collection(firestore(), NOTES_COLLECTION);
      const stops = [onSnapshot(query(notes, where("ownerId", "==", user.uid)), fill(own), onError)];
      if (user.email) {
        stops.push(onSnapshot(query(notes, where("sharedWith", "array-contains", user.email)), fill(shared), onError));
      }
      return () => stops.forEach((stop) => stop());
    },

    // Writes show up locally at once (and offline); the promises settle when the server confirms.
    async create(content, noteDate, checklist = []) {
      await setDoc(doc(collection(firestore(), NOTES_COLLECTION)), newNote(user, content, noteDate, checklist));
      // per-person activity for the users dashboard; never blocks or fails the note itself
      setDoc(doc(firestore(), USERS_COLLECTION, user.uid), { notesCreated: increment(1) }, { merge: true }).catch(() => {});
    },
    update: (note, changes) => updateDoc(noteRef(note.id), patchFor(note, changes)),
    updateMany(list, changes) {
      const batch = writeBatch(firestore());
      for (const note of list) batch.update(noteRef(note.id), patchFor(note, changes));
      return batch.commit();
    },
    setOrders(pairs) {
      const batch = writeBatch(firestore());
      for (const [note, order] of pairs) batch.update(noteRef(note.id), patchFor(note, { order }));
      return batch.commit();
    },
    /** Permanently deletes a note (and its screenshot). Normal deleting moves it to Trash via update({deleted}). */
    remove(note) {
      if (!note.attachment?.id) return deleteDoc(noteRef(note.id));
      const batch = writeBatch(firestore()); // the screenshot goes with its note
      batch.delete(noteRef(note.id));
      batch.delete(doc(firestore(), "qn_attachments", note.attachment.id));
      return batch.commit();
    },
    /** The full-size screenshot of a note, as a data: URL. */
    async getAttachment(id) {
      const snap = await getDoc(doc(firestore(), "qn_attachments", id));
      if (!snap.exists()) throw new Error("Screenshot not found");
      const a = snap.data();
      return `data:${a.mime};base64,${a.data}`;
    },
    /** Users dashboard: every profile, if the signed-in person is an admin (otherwise permission-denied). */
    async listUsers() {
      const snap = await getDocs(collection(firestore(), USERS_COLLECTION));
      return snap.docs.map((d) => {
        const u = d.data();
        const ts = (v) => (v?.toDate ? v.toDate() : v ? new Date(v) : null);
        return { ...u, createdAt: ts(u.createdAt), lastSignInAt: ts(u.lastSignInAt), lastSeenAt: ts(u.lastSeenAt),
          clients: Object.fromEntries(Object.entries(u.clients || {}).map(([k, c]) => [k, { ...c, lastSeenAt: ts(c.lastSeenAt) }])) };
      });
    },
    async isAdmin() {
      try { await getDocs(query(collection(firestore(), USERS_COLLECTION), limit(2))); return true; } catch { return false; }
    },
    setSharing: (note, emails) => updateDoc(noteRef(note.id), { sharedWith: emails, updated_at: nowIso() }),
  };
}
