// Sign-in page for the Mac app and the Chrome extension. They open this page in a
// sign-in window; after Google sign-in we hand the session's refresh token back
// through a callback only that window receives: quicknotes:// for the Mac app, or
// the extension's own chromiumapp.org address (only our extension's ID is accepted).

import {
  GoogleAuthProvider, getRedirectResult, signInWithRedirect, signOut,
} from "https://www.gstatic.com/firebasejs/10.14.1/firebase-auth.js";
import { auth } from "./store-firebase.js";
import { EXTENSION_ID } from "./firebase-config.js";

const $ = (id) => document.getElementById(id);

const requested = new URLSearchParams(location.search).get("return") || "";
const forExtension = requested.startsWith(`https://${EXTENSION_ID}.chromiumapp.org/`);
const callback = forExtension ? requested.split("#")[0] : "quicknotes://auth";
if (forExtension) {
  document.title = "Connect QuickNotes for Chrome";
  $("title").textContent = "Connect Chrome";
  $("message").textContent = "Sign in so the QuickNotes extension saves straight to your notes.";
}

function handBack(user) {
  $("message").textContent = `Connecting as ${user.email}…`;
  location.href = `${callback}#refresh_token=${encodeURIComponent(user.refreshToken)}`;
}

function startSignIn() {
  const provider = new GoogleAuthProvider();
  provider.setCustomParameters({ prompt: "select_account" });
  // a redirect rather than a popup: popups don't work inside the macOS sign-in sheet
  signInWithRedirect(auth, provider);
}

const result = await getRedirectResult(auth).catch((e) => {
  $("error").textContent = e.message;
  $("error").hidden = false;
  return null;
});
await auth.authStateReady();
const user = auth.currentUser;

if (result?.user) {
  handBack(result.user);                    // just came back from Google
} else if (user) {
  // already signed in on this browser: confirm which account to connect
  $("continue").textContent = `Continue as ${user.email}`;
  $("continue").hidden = false;
  $("continue").onclick = () => handBack(user);
  $("switch").hidden = false;
  $("switch").onclick = async () => { await signOut(auth); startSignIn(); };
} else {
  $("continue").textContent = "Continue with Google";
  $("continue").hidden = false;
  $("continue").onclick = startSignIn;
}
