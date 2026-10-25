// Package cloud is QuickNotes on Firestore: it loads the signed-in user's notes (their own plus
// those shared with them) and writes edits with exactly the fields the note rules produce.
// It sits between the MCP tools and the Firestore client, and is reusable by other programs
// (for example a command-line tool).
package cloud

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/MuhammadAmas/QuickNotes/mcp/internal/auth"
	"github.com/MuhammadAmas/QuickNotes/mcp/internal/firestore"
	"github.com/MuhammadAmas/QuickNotes/mcp/internal/notes"
)

// Collections used by every QuickNotes app.
const (
	NotesCollection = "qn_notes"
	UsersCollection = "users"
	maxCommit       = 400 // Firestore allows 500 writes per commit; stay below it
)

// ErrNotFound means the note does not exist or the user cannot see it.
var ErrNotFound = errors.New("note not found (it may have been deleted, or it isn't yours or shared with you)")

// Identity returns who is signed in.
type Identity func(ctx context.Context) (auth.User, error)

// Service reads and writes notes as the signed-in user.
type Service struct {
	fs       *firestore.Client
	identity Identity
	Now      func() time.Time
	Loc      *time.Location
}

// New returns a Service. now and loc default to the real clock and the Mac's time zone.
func New(fs *firestore.Client, identity Identity) *Service {
	return &Service{fs: fs, identity: identity, Now: time.Now, Loc: time.Local}
}

// Me returns the signed-in user as the note rules need it.
func (s *Service) Me(ctx context.Context) (notes.User, error) {
	u, err := s.identity(ctx)
	if err != nil {
		return notes.User{}, err
	}
	return notes.User{UID: u.UID, Email: u.Email, Name: u.Name}, nil
}

// Today is the current date in the Mac's time zone.
func (s *Service) Today() string { return notes.Today(s.Now(), s.Loc) }

// All returns every note the user can see, trashed ones included: their own and those shared with them.
func (s *Service) All(ctx context.Context) ([]notes.Note, notes.User, error) {
	me, err := s.Me(ctx)
	if err != nil {
		return nil, me, err
	}
	own, err := s.fs.RunQuery(ctx, NotesCollection, []firestore.Filter{{Field: "ownerId", Op: "EQUAL", Value: me.UID}})
	if err != nil {
		return nil, me, err
	}
	var shared []firestore.Document
	if me.Email != "" {
		if shared, err = s.fs.RunQuery(ctx, NotesCollection, []firestore.Filter{{Field: "sharedWith", Op: "ARRAY_CONTAINS", Value: me.Email}}); err != nil {
			return nil, me, err
		}
	}
	seen := map[string]bool{}
	out := []notes.Note{}
	for _, d := range append(own, shared...) {
		if !seen[d.ID] {
			seen[d.ID] = true
			out = append(out, FromDocument(d))
		}
	}
	return out, me, nil
}

// Get returns one note by id.
func (s *Service) Get(ctx context.Context, id string) (notes.Note, error) {
	doc, err := s.fs.Get(ctx, NotesCollection, id)
	if firestore.IsNotFound(err) || firestore.IsPermissionDenied(err) {
		return notes.Note{}, ErrNotFound
	}
	if err != nil {
		return notes.Note{}, err
	}
	return FromDocument(doc), nil
}

// Create adds a note for date; list lines in content become its checklist, as in the apps.
func (s *Service) Create(ctx context.Context, content, date string) (notes.Note, error) {
	me, err := s.Me(ctx)
	if err != nil {
		return notes.Note{}, err
	}
	text, checklist := notes.ParseChecklist(content)
	n := notes.NewNote(me, text, date, checklist, s.Now())
	n.ID = newID()
	if err := s.fs.Create(ctx, NotesCollection, n.ID, ToFields(n)); err != nil {
		return notes.Note{}, err
	}
	// users/{uid}.notesCreated += 1 for the users dashboard, like the other apps. A failure here
	// never undoes the note.
	if err := s.fs.Commit(ctx, []firestore.Write{{Document: s.fs.DocName(UsersCollection, me.UID), Increment: "notesCreated"}}); err != nil {
		log.Printf("quicknotes-mcp: note saved, but the notes-created counter was not updated: %v", err)
	}
	return n, nil
}

// Update applies changes to one note and returns the note as it is now.
func (s *Service) Update(ctx context.Context, n notes.Note, c notes.Changes) (notes.Note, error) {
	out, err := s.UpdateMany(ctx, []notes.Note{n}, c)
	if err != nil {
		return notes.Note{}, err
	}
	return out[0], nil
}

// UpdateMany applies the same changes to several notes atomically (up to 400 per commit).
func (s *Service) UpdateMany(ctx context.Context, list []notes.Note, c notes.Changes) ([]notes.Note, error) {
	now := s.Now()
	out := make([]notes.Note, 0, len(list))
	for start := 0; start < len(list); start += maxCommit {
		end := min(start+maxCommit, len(list))
		writes := make([]firestore.Write, 0, end-start)
		var batch []notes.Note
		for _, n := range list[start:end] {
			p := notes.PatchFor(n, c, now)
			writes = append(writes, firestore.Write{Document: s.fs.DocName(NotesCollection, n.ID), Fields: patchFields(p)})
			batch = append(batch, notes.Apply(n, p))
		}
		if err := s.fs.Commit(ctx, writes); err != nil {
			if firestore.IsPermissionDenied(err) {
				return out, errors.New("QuickNotes refused the change: you can only change notes you own or that are shared with you, and only the owner can trash or restore a note")
			}
			return out, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

// patchFields converts a patch's checklist into plain maps for the Firestore encoder.
func patchFields(p notes.Patch) map[string]any {
	fields := make(map[string]any, len(p))
	for k, v := range p {
		if items, ok := v.([]notes.ChecklistItem); ok {
			v = checklistValue(items)
		}
		fields[k] = v
	}
	return fields
}

func checklistValue(items []notes.ChecklistItem) []any {
	out := make([]any, len(items))
	for i, it := range items {
		out[i] = map[string]any{"id": it.ID, "text": it.Text, "done": it.Done}
	}
	return out
}

// newID returns a random UUID (v4) for a new note document, as the Mac app uses.
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
