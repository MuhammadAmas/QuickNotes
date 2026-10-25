// Oracle "browser sign-in fallback is safe", web side: which addresses /link will post a session to,
// and that a local program never gets one without a click. Run: node --test link-return.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { localHandBackForm, loopbackReturn, nextStep } from "../../../../web/link-return.js";

const STATE = "a".repeat(64);

test("accepts only exact loopback callbacks", () => {
  for (const ok of ["http://127.0.0.1:53682/callback", "http://[::1]:1024/callback", "http://127.0.0.1:65535/callback"]) {
    assert.ok(loopbackReturn(ok, STATE), ok);
  }
  assert.equal(loopbackReturn("http://127.0.0.1:53682/callback", STATE).port, 53682);
});

test("rejects everything else", () => {
  const bad = [
    "", "quicknotes://auth", "https://127.0.0.1:53682/callback", "http://localhost:53682/callback",
    "http://127.0.0.2:53682/callback", "http://127.0.0.1.evil.example:53682/callback", "http://evil.example/callback",
    "http://127.0.0.1:80@evil.example/callback", "http://user@127.0.0.1:53682/callback", "http://127.0.0.1/callback",
    "http://127.0.0.1:80/callback", "http://127.0.0.1:1023/callback", "http://127.0.0.1:65536/callback", "http://127.0.0.1:099999/callback",
    "http://127.0.0.1:053682/callback", "http://127.0.0.1:53682/callback/", "http://127.0.0.1:53682/other",
    "http://127.0.0.1:53682/callback?x=1", "http://127.0.0.1:53682/callback#x", "HTTP://127.0.0.1:53682/callback",
    " http://127.0.0.1:53682/callback", "http://127.0.0.1:53682/callback\n", "http://0x7f000001:53682/callback",
    "http://[::ffff:127.0.0.1]:53682/callback", "http://2130706433:53682/callback", "javascript:alert(1)//127.0.0.1:5000/callback",
  ];
  for (const url of bad) assert.equal(loopbackReturn(url, STATE), null, JSON.stringify(url));
});

test("requires a well-formed one-time state from the program", () => {
  const url = "http://127.0.0.1:53682/callback";
  for (const state of [null, "", "short", "a".repeat(31), "a".repeat(129), "x".repeat(40) + "<script>", "a".repeat(40) + " "]) {
    assert.equal(loopbackReturn(url, state), null, JSON.stringify(state));
  }
});

test("a local program always needs a click; the Mac app and extension keep their behaviour", () => {
  const local = loopbackReturn("http://127.0.0.1:53682/callback", STATE);
  assert.equal(nextStep({ justSignedIn: true, signedIn: true, local }), "confirm");
  assert.equal(nextStep({ justSignedIn: false, signedIn: true, local }), "confirm");
  assert.equal(nextStep({ justSignedIn: false, signedIn: false, local }), "signIn");
  assert.equal(nextStep({ justSignedIn: true, signedIn: true, local: null }), "handBack");
  assert.equal(nextStep({ justSignedIn: false, signedIn: true, local: null }), "confirm");
  assert.equal(nextStep({ justSignedIn: false, signedIn: false, local: null }), "signIn");
});

test("the session travels in a POST body, never in the URL", () => {
  const made = [];
  const doc = { createElement: (tag) => { const el = { tag, children: [], append(c) { this.children.push(c); } }; made.push(el); return el; } };
  const local = loopbackReturn("http://127.0.0.1:53682/callback", STATE);
  const form = localHandBackForm(doc, local, "REFRESH-SECRET");
  assert.equal(form.method, "POST");
  assert.equal(form.action, "http://127.0.0.1:53682/callback");
  assert.ok(!form.action.includes("REFRESH-SECRET"));
  assert.deepEqual(form.children.map((i) => [i.type, i.name, i.value]), [["hidden", "refresh_token", "REFRESH-SECRET"], ["hidden", "state", STATE]]);
});
