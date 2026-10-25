package tools

import (
	"github.com/MuhammadAmas/QuickNotes/mcp/internal/notes"
)

// ChecklistItem is one checklist line as tools return it.
type ChecklistItem struct {
	ID   string `json:"id" jsonschema:"pass this as item_id to update_checklist_item"`
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// Note is a note as tools return it. Internal fields (owner ids, order numbers) are left out.
type Note struct {
	ID            string          `json:"id" jsonschema:"note id; pass it to get_note, set_status, move_notes or trash_note"`
	Content       string          `json:"content"`
	Date          string          `json:"date" jsonschema:"the day the note belongs to, YYYY-MM-DD"`
	Status        string          `json:"status" jsonschema:"open or done"`
	Mine          bool            `json:"mine" jsonschema:"false when someone else shared this note with you"`
	Owner         string          `json:"owner,omitempty" jsonschema:"who shared it, when it isn't yours"`
	SharedWith    []string        `json:"shared_with,omitempty"`
	Checklist     []ChecklistItem `json:"checklist,omitempty"`
	TimesMoved    int             `json:"times_moved,omitempty" jsonschema:"how often it was moved to another day"`
	OriginalDate  string          `json:"original_date,omitempty" jsonschema:"the day it was first planned for, if moved"`
	CreatedAt     string          `json:"created_at"`
	CompletedAt   string          `json:"completed_at,omitempty"`
	InTrash       bool            `json:"in_trash,omitempty"`
	HasScreenshot bool            `json:"has_screenshot,omitempty"`
}

func noteOut(n notes.Note, uid string) Note {
	out := Note{
		ID: n.ID, Content: n.Content, Date: n.NoteDate, Status: n.Status, Mine: n.OwnerID == uid,
		TimesMoved: n.TimesMoved, OriginalDate: deref(n.OriginalDate), CreatedAt: n.CreatedAt,
		CompletedAt: deref(n.CompletedAt), InTrash: notes.Trashed(n), HasScreenshot: n.Attachment != nil,
	}
	if !out.Mine {
		out.Owner = n.OwnerName
		if out.Owner == "" {
			out.Owner = n.OwnerEmail
		}
	}
	if len(n.SharedWith) > 0 {
		out.SharedWith = n.SharedWith
	}
	for _, it := range n.Checklist {
		out.Checklist = append(out.Checklist, ChecklistItem(it))
	}
	return out
}

func notesOut(list []notes.Note, uid string) []Note {
	out := make([]Note, 0, len(list))
	for _, n := range list {
		out = append(out, noteOut(n, uid))
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
