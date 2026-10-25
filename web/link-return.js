// Rules for handing a session from the /link page to a program on this Mac (quicknotes-mcp login).
// Kept free of Firebase so it can be tested on its own (mcp/oracle-tests/scenarios/loopback-signin-safety).

// Exactly http://127.0.0.1:<port>/callback or http://[::1]:<port>/callback: no other host name, no
// user@ part, no extra path, query or fragment. Matched as text on purpose: a URL parser would turn
// tricks like "http://127.0.0.1:80@evil.example/callback" into a different host.
const LOOPBACK = /^http:\/\/(127\.0\.0\.1|\[::1\]):([1-9][0-9]{3,4})\/callback$/;
const STATE = /^[A-Za-z0-9_-]{32,128}$/;

/**
 * Returns { url, port, state } when `requested` is a loopback callback this page may post a session
 * to (with a well-formed one-time `state` from the program), or null otherwise.
 */
export function loopbackReturn(requested, state) {
  const m = LOOPBACK.exec(String(requested || ""));
  if (!m || !STATE.test(String(state || ""))) return null;
  const port = Number(m[2]);
  if (port < 1024 || port > 65535) return null;
  return { url: requested, port, state };
}

/**
 * What the page does next:
 *  - "confirm": show "Allow …" and wait for a click (always the case for a local program);
 *  - "handBack": return the session straight away (the Mac app and the extension, right after sign-in);
 *  - "signIn": nobody is signed in yet.
 */
export function nextStep({ justSignedIn, signedIn, local }) {
  if (!signedIn) return "signIn";
  if (justSignedIn && !local) return "handBack";
  return "confirm";
}

/** Builds the form that posts the session to the local program (body, not URL, so it stays out of history). */
export function localHandBackForm(doc, local, refreshToken) {
  const form = doc.createElement("form");
  form.method = "POST";
  form.action = local.url;
  for (const [name, value] of [["refresh_token", refreshToken], ["state", local.state]]) {
    const input = doc.createElement("input");
    input.type = "hidden";
    input.name = name;
    input.value = value;
    form.append(input);
  }
  return form;
}
