// Reference outputs from the real web/notes-model.js, with the clock frozen at fixtures.now.
// Usage: node reference.mjs <fixtures.json> <notes-model.js>
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const [fixturesPath, modelPath] = process.argv.slice(2);
const fx = JSON.parse(readFileSync(fixturesPath, "utf8"));
const FIXED = Date.parse(fx.now);
const RealDate = Date;
globalThis.Date = class extends RealDate {
  constructor(...args) { super(...(args.length ? args : [FIXED])); }
  static now() { return FIXED; }
};
const m = await import(pathToFileURL(modelPath).href);

const many = Array.from({ length: fx.many_items }, (_, i) => `- item ${i}`).join("\n");
const out = {
  parse: [...fx.parse, many].map((t) => m.parseChecklist(t)),
  new_note: fx.new_note.map((c) => {
    const parsed = c.parse ? m.parseChecklist(c.parse) : null;
    return parsed ? m.newNote(fx.user, parsed.content, c.date, parsed.checklist) : m.newNote(fx.user, c.content, c.date);
  }),
  patch: fx.patch.map((c) => m.patchFor(c.note, c.changes)),
  emails: fx.emails.map((l) => m.normalizeEmails(l)),
};
process.stdout.write(JSON.stringify(out));
