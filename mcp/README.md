# quicknotes-mcp

An [MCP](https://modelcontextprotocol.io) server, written in Go, that lets AI assistants (Claude Code, Claude Desktop, MCP Inspector) read and organise your QuickNotes. It signs in as **you**, so your Firestore security rules apply to every request. You see and change exactly what you can in the QuickNotes apps, and nothing more.

Built on the official Go SDK, [`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) v1.8.0, talking to clients over **stdio**.

## What it offers

| Tool | What it does |
|---|---|
| `list_notes` | One day's notes (default today), or `view` = `open` / `done` / `shared` / `week`, ordered like the app |
| `search_notes` | Find text in notes, checklist items and owner names across every day |
| `get_note` | One note with its checklist, sharing and move history |
| `weekly_summary` | The app's Weekly summary: done (vs previous week), added, % finished, overdue, keeps slipping, per day |
| `add_note` | Add a note to today or any day. Lines like `- milk` or `[ ] call Sam` become a checklist |
| `set_status` | Mark a note done, or open again |
| `move_notes` | Move up to 50 notes to any date (all or nothing) |
| `move_unfinished_to_tomorrow` | Move a day's unfinished notes to the next day |
| `update_checklist_item` | Tick, un-tick or reword one checklist item |
| `trash_note` *(destructive)* | Move your own note to the Trash (restorable for 30 days), or restore it |

There's also a **resource**, `quicknotes://today` (today's notes as JSON, for attaching as context), and a **prompt**, `weekly-review` (asks the AI to review your week and suggest what to finish, move or drop, checking with you before it changes anything).

Notes shared with you are included, as in the web app. Only the owner can trash or restore a note. Nothing is ever permanently deleted by this server.

## How it fits together

```
AI client ──stdin/stdout (JSON-RPC)──▶ quicknotes-mcp
                                         cmd/server        wiring, stdio, `login` command
                                         internal/tools    input checks, MCP tools/resource/prompt
                                         internal/cloud    notes service: load own + shared, write patches
                                         internal/notes    note rules, ported from web/notes-model.js
                                         internal/firestore  Firestore REST client (no MCP, no notes)
                                         internal/auth     sign-in: session files, ID tokens, browser login
                                            │
                                            ├─▶ securetoken.googleapis.com   refresh token → ID token
                                            └─▶ firestore.googleapis.com     as you, under your security rules
logs ─▶ stderr only (stdout is the protocol)
```

Dependencies point one way: `cmd/server` → `tools` → `cloud` → `notes` / `firestore` / `auth`. `internal/fakefirebase` is the in-memory stand-in used by the tests.

## Requirements

- Go 1.27 or newer
- The private `quicknotes.config.json` at the repository root (see the main README). The server reads `firebase.apiKey`, `firebase.projectId` and `webURL` from it.
- To sign in: the QuickNotes Mac app signed in, **or** a one-time `quicknotes-mcp login`.
- Node.js only for the web-side tests and MCP Inspector.

## Build

```bash
cd mcp
go build -o bin/quicknotes-mcp ./cmd/server
```

When the binary sits in `mcp/bin/`, it finds `../../quicknotes.config.json` by itself. Elsewhere, set `QUICKNOTES_CONFIG=/path/to/quicknotes.config.json`, or set `QUICKNOTES_API_KEY` and `QUICKNOTES_PROJECT_ID` (plus `QUICKNOTES_WEB_URL` for `login`).

## Sign-in

1. **Mac app session (default).** If the QuickNotes bubble is signed in, the server reads `~/Library/Application Support/QuickNotes/session.json`. It only ever *reads* that file and never writes, moves or deletes it, so it can't sign the Mac app out.
2. **Browser fallback.** Without the Mac app, run this once in a terminal:

   ```bash
   mcp/bin/quicknotes-mcp login
   ```

   Your browser opens the QuickNotes sign-in page, which asks you to confirm that *a program on this Mac* may use your notes. After you click **Allow**, the page posts the session to `http://127.0.0.1:<port>/callback`, which the command listens on for up to 5 minutes. The session is saved to `~/Library/Application Support/QuickNotes/mcp-session.json` with owner-only permissions (`0600`). Delete that file to sign the server out.

Tokens are kept in memory or in those files only. They never appear in logs, tool results or error messages.

## Use it from an AI client

### Claude Code

Register the server in your **personal** config (the path is specific to your Mac, so it doesn't belong in the repo):

```bash
claude mcp add quicknotes -- "$PWD/bin/quicknotes-mcp"
```

Then start a new session and try *"What's still open from this week in QuickNotes?"*

### Claude Desktop

Settings → Developer → Edit Config, and add:

```json
{ "mcpServers": { "quicknotes": { "command": "/absolute/path/to/QuickNotes/mcp/bin/quicknotes-mcp" } } }
```

Quit Claude completely (Cmd+Q) and reopen it. Logs go to `~/Library/Logs/Claude/mcp-server-quicknotes.log`.

**After every rebuild,** start a new session or restart the app, because clients keep the old process running.

### MCP Inspector

```bash
npx @modelcontextprotocol/inspector ./bin/quicknotes-mcp
```

## Tests

```bash
cd mcp
go test ./...                 # everything (no network, no real account)
go test ./... -race -cover
oracle-tests/run.sh           # same as go test -race ./...
```

The tests never touch your real notes. They run against `internal/fakefirebase`, an in-memory securetoken and Firestore that enforces the security rules the tools depend on.

| Behavior test (Oracle) | Where |
|---|---|
| Go note rules give exactly the same results as `web/notes-model.js` (run under Node) | `internal/notes/parity_test.go` |
| Read journey: every read tool, the resource and the prompt, through a real MCP client | `cmd/server/main_test.go` |
| Write journey: add → done → tick → move → move unfinished → trash → restore, with exact Firestore field masks | `cmd/server/main_test.go` |
| Bad input is refused with a clear message and **no** Firestore call | `cmd/server/main_test.go` |
| Canary tokens never appear on stdout, stderr or in errors (the real binary over stdio) | `cmd/server/stdio_test.go`, `internal/auth`, `internal/firestore` |
| The Mac `session.json` keeps the same bytes, permissions and timestamp | `cmd/server/stdio_test.go`, `internal/auth` |
| Browser sign-in: loopback only, one-time state, single use, timeout, `0600` file; `/link` accepts only exact loopback callbacks and always asks first | `internal/auth/login_test.go`, `oracle-tests/scenarios/loopback-signin-safety/` |

Each test has a portable descriptor in `oracle-tests/scenarios/<name>/oracle.json`. The real-account check (`live-account-smoke`) is a manual walkthrough.

## Adding a tool

1. If it needs new data access, add it to `internal/cloud` (and `internal/firestore` if new REST calls are needed), with tests against `internal/fakefirebase`.
2. In `internal/tools`, add an input struct, an output struct, a logic function `Xxx(ctx, svc, in)` and `AddXxxTool(server, svc)`. Add ranges and enums in the `inputSchema` customizer.
3. Call it from `tools.Register`.
4. Extend `cmd/server/main_test.go`, then run `go test ./... -race`.

## Troubleshooting

| Symptom | Fix |
|---|---|
| `QuickNotes settings not found` | Run from `mcp/bin/`, or set `QUICKNOTES_CONFIG` |
| Tool says `not signed in to QuickNotes` | Sign in from the Mac bubble, or run `quicknotes-mcp login` |
| Client shows old tools | Rebuild, then start a new session |
| Connection closes immediately | Something wrote to stdout: use `log` (stderr), never `fmt.Println` |
| `login` opens the page, but it says "Connect your Mac" | The live web app doesn't have the updated `/link` page yet: deploy it (`./deploy.sh`) |
