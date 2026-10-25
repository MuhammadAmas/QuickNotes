# Changelog

All notable changes to QuickNotes are documented in this file. The project has no tagged
releases yet, so changes are grouped by date. Entries before 2026-10-17 were reconstructed from
the Git history.

## [Unreleased]

### Added
- Keyboard triage in the web app: <kbd>j</kbd>/<kbd>k</kbd> to select a note, <kbd>x</kbd> to tick,
  <kbd>m</kbd> to move, <kbd>e</kbd> to edit, <kbd>⌫</kbd> to trash (with Undo), and <kbd>?</kbd>
  for the list.
- `scripts/ui-check`: an automated check of accessibility, contrast, tap sizes, phone density,
  the keyboard shortcuts and the existing flows, for every view and the Chrome popup.
- **quicknotes-mcp**, an MCP server written in Go (`mcp/`), so AI assistants such as Claude can
  work with your notes. It has 10 tools: list, search, get and weekly summary; add, mark done/open,
  move to any date, move unfinished to tomorrow, tick checklist items, and trash/restore. It also
  offers a `quicknotes://today` resource and a `weekly-review` prompt. It signs in with the Mac
  app's saved session and never changes it.
- `quicknotes-mcp login`: a browser sign-in for when the Mac app isn't signed in. The `/link` page
  now accepts a local program's callback on `127.0.0.1`, asks you to confirm first, and hands the
  session over in a form post rather than in the URL.

### Changed
- Easier to read everywhere: all text in the web app and Chrome popup now meets WCAG AA contrast
  in light and dark themes. Secondary text is darker, and orange text is a deeper shade while
  buttons, rings and icons keep the brand orange.
- Easier to use on a phone: every tap target is at least 24 px and the main actions at least 44 pt.
  Each note gets one "⋯" menu instead of a row of icons, so twice as many notes fit on screen.
- The Chrome popup has a proper heading and main region for screen readers.

## 2026-10-15

### Added
- Loading placeholders while notes are fetched, in the web app, the users dashboard and the Chrome popup.

### Changed
- Web app pages are no longer cached by the hosting CDN, so updates show up right after a deploy.

## 2026-10-14

### Added
- Move notes to any date from a "Move to" menu, not just tomorrow.

## 2026-10-13

### Added
- Daily and weekly reminders on the Mac (morning, evening and Friday review).
- Voice notes: dictate a note into the Mac quick-note box (⌃⌥V).
- Week view (Monday to Sunday) with drag-and-drop between days.
- Weekly summary: done vs the previous week, added, finished share, overdue and notes that keep slipping.
- Checklists inside notes: list lines such as `- milk` or `[ ] call Sam` become tickable items.
- Trash with restore for 30 days.
- Users dashboard for admins, with sign-up and app charts and CSV export.
- Links in notes are clickable, including bare addresses like `example.com`.

### Changed
- Firestore rules are now generated from `firestore.rules.template`, with admin emails coming from
  the private config.

## 2026-10-12

### Added
- Full-screen screenshot notes (⌃⌥F) alongside area screenshots (⌃⌥S).

## 2026-10-11

### Added
- First version of QuickNotes:
  - a floating bubble on the Mac with a quick-note box (⌃⌥N / ⌘⇧N) that saves under today's date;
  - a web app for phone and desktop, installable, with notes by day that you can tick, edit,
    reorder by dragging and move to the next day, plus a dark theme;
  - a Chrome extension;
  - Google sign-in, sync through Firestore, sharing notes by email, and screenshot notes on the
    Mac and in Chrome.
