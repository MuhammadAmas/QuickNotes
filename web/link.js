// Sign-in page for the Mac app, the Chrome extension and quicknotes-mcp. The apps open this page in a
// sign-in window; after Google sign-in we hand the session's refresh token back
// through a callback only that window receives: quicknotes:// for the Mac app, or
// the extension's own chromiumapp.org address (only our extension's ID is accepted).
// quicknotes-mcp login asks for http://127.0.0.1:<port>/callback instead: that program gets the
// session only after the person clicks "Allow", as a form POST (see link-return.js).

import {
  GoogleAuthProvider, getRedirectResult, signInWithRedirect, signOut,
} from "https://www.gstatic.com/firebasejs/10.14.1/firebase-auth.js";
import { auth } from "./store-firebase.js";
import { EXTENSION_ID } from "./firebase-config.js";
import { localHandBackForm, loopbackReturn, nextStep } from "./link-return.js";

const $ = (id) => document.getElementById(id);

const params = new URLSearchParams(location.search);
const requested = params.get("return") || "";
const local = loopbackReturn(requested, params.get("state"));
const forExtension = !local && requested.startsWith(`https://${EXTENSION_ID}.chromiumapp.org/`);
const callback = forExtension ? requested.split("#")[0] : "quicknotes://auth";
if (local) {
  document.title = "Connect a program on this Mac";
  $("title").textContent = "Connect a program on this Mac";
  $("message").textContent = `quicknotes-mcp, a program on this Mac (port ${local.port}), wants to read and change your notes. ` +
    "Only continue if you just ran “quicknotes-mcp login” yourself.";
} else if (forExtension) {
  document.title = "Connect QuickNotes for Chrome";
  $("title").textContent = "Connect Chrome";
  $("message").textContent = "Sign in so the QuickNotes extension saves straight to your notes.";
}

function handBack(user) {
  $("message").textContent = `Connecting as ${user.email}…`;
  if (local) {
    const form = localHandBackForm(document, local, user.refreshToken);
    document.body.append(form);
    form.submit();
    return;
  }
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

const step = nextStep({ justSignedIn: Boolean(result?.user), signedIn: Boolean(user), local });
if (step === "handBack") {
  handBack(result.user);                    // just came back from Google
} else if (step === "confirm") {
  // already signed in on this browser (or a local program asked): confirm which account to connect
  $("continue").textContent = local ? `Allow and continue as ${user.email}` : `Continue as ${user.email}`;
  $("continue").hidden = false;
  $("continue").onclick = () => handBack(user);
  $("switch").hidden = false;
  $("switch").onclick = async () => { await signOut(auth); startSignIn(); };
} else {
  $("continue").textContent = "Continue with Google";
  $("continue").hidden = false;
  $("continue").onclick = startSignIn;
}
