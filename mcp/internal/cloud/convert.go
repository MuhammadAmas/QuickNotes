package cloud

import (
	"github.com/MuhammadAmas/QuickNotes/mcp/internal/firestore"
	"github.com/MuhammadAmas/QuickNotes/mcp/internal/notes"
)

// FromDocument reads a qn_notes document, tolerating missing or older fields.
func FromDocument(d firestore.Document) notes.Note {
	f := d.Fields
	n := notes.Note{
		ID:           d.ID,
		OwnerID:      str(f["ownerId"]),
		OwnerEmail:   str(f["ownerEmail"]),
		OwnerName:    str(f["ownerName"]),
		Content:      str(f["content"]),
		NoteDate:     str(f["note_date"]),
		Status:       str(f["status"]),
		CreatedAt:    str(f["created_at"]),
		UpdatedAt:    str(f["updated_at"]),
		EditedAt:     optStr(f["edited_at"]),
		CompletedAt:  optStr(f["completed_at"]),
		OriginalDate: optStr(f["original_date"]),
		DeletedAt:    optStr(f["deleted_at"]),
		TimesMoved:   int(num(f["times_moved"])),
		SharedWith:   []string{},
	}
	if n.Status == "" {
		n.Status = notes.StatusOpen
	}
	if v, ok := f["order"]; ok && v != nil {
		o := num(v)
		n.Order = &o
	}
	if list, ok := f["sharedWith"].([]any); ok {
		for _, e := range list {
			n.SharedWith = append(n.SharedWith, str(e))
		}
	}
	if list, ok := f["checklist"].([]any); ok {
		for _, raw := range list {
			if m, ok := raw.(map[string]any); ok {
				done, _ := m["done"].(bool)
				n.Checklist = append(n.Checklist, notes.ChecklistItem{ID: str(m["id"]), Text: str(m["text"]), Done: done})
			}
		}
	}
	if m, ok := f["attachment"].(map[string]any); ok {
		n.Attachment = &notes.Attachment{ID: str(m["id"]), Kind: str(m["kind"]), Width: int(num(m["width"])), Height: int(num(m["height"]))}
	}
	return n
}

// ToFields is the document for a new note (the id lives in the document name, not its fields).
func ToFields(n notes.Note) map[string]any {
	fields := map[string]any{
		"ownerId": n.OwnerID, "ownerEmail": n.OwnerEmail, "ownerName": n.OwnerName,
		"content": n.Content, "note_date": n.NoteDate, "status": n.Status,
		"created_at": n.CreatedAt, "updated_at": n.UpdatedAt,
		"edited_at": ptr(n.EditedAt), "completed_at": ptr(n.CompletedAt), "original_date": ptr(n.OriginalDate),
		"times_moved": n.TimesMoved, "sharedWith": n.SharedWith,
	}
	if n.Order != nil {
		fields["order"] = *n.Order
	}
	if len(n.Checklist) > 0 {
		fields["checklist"] = checklistValue(n.Checklist)
	}
	return fields
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func optStr(v any) *string {
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

func ptr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func num(v any) float64 {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case float64:
		return x
	case int:
		return float64(x)
	}
	return 0
}
