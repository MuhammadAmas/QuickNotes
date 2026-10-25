// Package notes holds the QuickNotes note rules, ported from web/notes-model.js:
// the shape of a new note and the exact fields each kind of edit writes.
// The web, Mac and Chrome apps write the same fields, so notes created or changed
// here look identical to theirs. This package knows nothing about Firestore or MCP.
package notes

import (
	"crypto/rand"
	"math/big"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
)

// Limits shared with the apps and the Firestore security rules.
const (
	MaxContent   = 10000 // characters (UTF-16 units, like the apps' maxlength)
	MaxShares    = 50
	MaxChecklist = 100
	MaxItemText  = 500
	TrashDays    = 30
)

// Note statuses.
const (
	StatusOpen = "open"
	StatusDone = "done"
)

// ChecklistItem is one tickable line inside a note.
type ChecklistItem struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// Attachment describes a screenshot attached to a note (the full image lives in qn_attachments).
type Attachment struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Note is one document of the qn_notes collection. Pointer fields are null when unset.
type Note struct {
	ID           string          `json:"id"`
	OwnerID      string          `json:"ownerId"`
	OwnerEmail   string          `json:"ownerEmail"`
	OwnerName    string          `json:"ownerName"`
	Content      string          `json:"content"`
	NoteDate     string          `json:"note_date"`
	Status       string          `json:"status"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
	EditedAt     *string         `json:"edited_at"`
	CompletedAt  *string         `json:"completed_at"`
	OriginalDate *string         `json:"original_date"`
	TimesMoved   int             `json:"times_moved"`
	SharedWith   []string        `json:"sharedWith"`
	Order        *float64        `json:"order,omitempty"`
	Checklist    []ChecklistItem `json:"checklist,omitempty"`
	DeletedAt    *string         `json:"deleted_at,omitempty"`
	Attachment   *Attachment     `json:"attachment,omitempty"`
}

// User is the signed-in person a new note belongs to.
type User struct {
	UID   string
	Email string
	Name  string
}

// Timestamp formats t the way JavaScript's Date.toISOString does: UTC with milliseconds.
func Timestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// NewNote returns a fresh open note for date (YYYY-MM-DD), matching newNote() in notes-model.js.
// New notes get order = now in milliseconds, so they sort to the bottom of their day.
func NewNote(user User, content, date string, checklist []ChecklistItem, now time.Time) Note {
	ts := Timestamp(now)
	order := float64(now.UnixMilli())
	n := Note{
		OwnerID:    user.UID,
		OwnerEmail: user.Email,
		OwnerName:  user.Name,
		Content:    content,
		NoteDate:   date,
		Status:     StatusOpen,
		CreatedAt:  ts,
		UpdatedAt:  ts,
		SharedWith: []string{},
		Order:      &order,
	}
	if len(checklist) > 0 {
		n.Checklist = checklist
	}
	return n
}

// Changes lists the edits to apply; nil fields are left alone.
type Changes struct {
	Content   *string
	Status    *string
	NoteDate  *string
	Order     *float64
	Checklist *[]ChecklistItem
	Deleted   *bool // true moves the note to Trash, false restores it
}

// Patch maps Firestore field names to their new values (nil means null).
type Patch map[string]any

// PatchFor returns the fields to write for changes applied to note, matching patchFor() in
// notes-model.js: unchanged content, status or date write nothing for that field, and every
// patch sets updated_at.
func PatchFor(note Note, c Changes, now time.Time) Patch {
	ts := Timestamp(now)
	p := Patch{"updated_at": ts}
	if c.Content != nil && *c.Content != note.Content {
		p["content"] = *c.Content
		p["edited_at"] = ts
	}
	if c.Status != nil && *c.Status != note.Status {
		p["status"] = *c.Status
		if *c.Status == StatusDone {
			p["completed_at"] = ts
		} else {
			p["completed_at"] = nil
		}
	}
	if c.Order != nil {
		p["order"] = *c.Order
	}
	if c.Checklist != nil {
		items := *c.Checklist
		if len(items) > MaxChecklist {
			items = items[:MaxChecklist]
		}
		p["checklist"] = items
	}
	if c.Deleted != nil {
		if *c.Deleted {
			p["deleted_at"] = ts
		} else {
			p["deleted_at"] = nil
		}
	}
	if c.NoteDate != nil && *c.NoteDate != note.NoteDate {
		p["note_date"] = *c.NoteDate
		original := note.NoteDate
		if note.OriginalDate != nil && *note.OriginalDate != "" {
			original = *note.OriginalDate
		}
		p["original_date"] = original
		p["times_moved"] = note.TimesMoved + 1
	}
	return p
}

// Apply returns note with patch p written into it, so callers can show the result of an edit.
func Apply(note Note, p Patch) Note {
	str := func(v any) *string {
		if s, ok := v.(string); ok {
			return &s
		}
		return nil
	}
	for field, v := range p {
		switch field {
		case "updated_at":
			note.UpdatedAt, _ = v.(string)
		case "content":
			note.Content, _ = v.(string)
		case "edited_at":
			note.EditedAt = str(v)
		case "status":
			note.Status, _ = v.(string)
		case "completed_at":
			note.CompletedAt = str(v)
		case "order":
			if f, ok := v.(float64); ok {
				note.Order = &f
			}
		case "checklist":
			note.Checklist, _ = v.([]ChecklistItem)
		case "deleted_at":
			note.DeletedAt = str(v)
		case "note_date":
			note.NoteDate, _ = v.(string)
		case "original_date":
			note.OriginalDate = str(v)
		case "times_moved":
			note.TimesMoved, _ = v.(int)
		}
	}
	return note
}

// emailRE matches /^[^\s@]+@[^\s@]+\.[^\s@]+$/ from notes-model.js.
var emailRE = regexp.MustCompile(`^[^` + jsSpace + `@]+@[^` + jsSpace + `@]+\.[^` + jsSpace + `@]+$`)

// ValidEmail reports whether s looks like an email address, using the apps' rule.
func ValidEmail(s string) bool { return emailRE.MatchString(s) }

// NormalizeEmails trims, lowercases, validates and de-duplicates addresses (first 50 kept).
func NormalizeEmails(list []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, e := range list {
		e = strings.ToLower(TrimJS(e))
		if !ValidEmail(e) || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	if len(out) > MaxShares {
		out = out[:MaxShares]
	}
	return out
}

// NewItemID returns a random 8-character id like the apps' checklist item ids.
func NewItemID() string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 8)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			panic(err) // crypto/rand never fails on supported platforms
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b)
}

// NewChecklistItem returns an item with a fresh id and text cut to MaxItemText characters.
func NewChecklistItem(text string, done bool) ChecklistItem {
	return ChecklistItem{ID: NewItemID(), Text: TruncateUTF16(text, MaxItemText), Done: done}
}

// LenUTF16 counts characters the way JavaScript's string.length does.
func LenUTF16(s string) int { return len(utf16.Encode([]rune(s))) }

// TruncateUTF16 keeps at most n UTF-16 units of s, like JavaScript's s.slice(0, n), except that it
// never splits an emoji or other surrogate pair in half.
func TruncateUTF16(s string, n int) string {
	units := 0
	for i, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if units+w > n {
			return s[:i]
		}
		units += w
	}
	return s
}
