package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MuhammadAmas/QuickNotes/mcp/internal/cloud"
	"github.com/MuhammadAmas/QuickNotes/mcp/internal/notes"
)

// writeHints marks a tool that changes notes but never deletes anything.
func writeHints(title string, idempotent bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: jsonschema.Ptr(false), IdempotentHint: idempotent, OpenWorldHint: jsonschema.Ptr(false)}
}

// loadLive gets a note that may be edited: it must exist, be visible and not be in the Trash.
func loadLive(ctx context.Context, svc *cloud.Service, id, doing string) (notes.Note, error) {
	n, err := svc.Get(ctx, id)
	if err != nil {
		return notes.Note{}, friendly(err, doing)
	}
	if notes.Trashed(n) {
		return notes.Note{}, fmt.Errorf("note %s is in the Trash; restore it with trash_note restore=true first", id)
	}
	return n, nil
}

// ---------- add_note ----------

// AddNoteInput is the argument schema for add_note.
type AddNoteInput struct {
	Content string `json:"content" jsonschema:"note text. Lines like '- milk', '* eggs', '[ ] call Sam' or '[x] done' become checklist items"`
	Date    string `json:"date,omitempty" jsonschema:"day to file it under, YYYY-MM-DD; default today"`
}

// AddNote is the tool's logic.
func AddNote(ctx context.Context, svc *cloud.Service, in AddNoteInput) (Note, error) {
	content, err := validateText("content", in.Content, notes.MaxContent)
	if err != nil {
		return Note{}, err
	}
	date, err := resolveDate("date", in.Date, svc.Today())
	if err != nil {
		return Note{}, err
	}
	n, err := svc.Create(ctx, content, date)
	if err != nil {
		return Note{}, friendly(err, "save the note")
	}
	return noteOut(n, n.OwnerID), nil
}

// AddAddNoteTool registers add_note.
func AddAddNoteTool(server *mcp.Server, svc *cloud.Service) {
	tool := &mcp.Tool{
		Name:        "add_note",
		Description: "Add a note to QuickNotes for today or another day. It appears at the bottom of that day in every QuickNotes app.",
		Annotations: writeHints("Add note", false),
		InputSchema: inputSchema[AddNoteInput](func(p map[string]*jsonschema.Schema) {
			lengthProp(p["content"], 1, notes.MaxContent)
			dateProp(p["date"])
		}),
	}
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in AddNoteInput) (*mcp.CallToolResult, Note, error) {
		out, err := AddNote(ctx, svc, in)
		return nil, out, err
	})
}

// ---------- set_status ----------

// SetStatusInput is the argument schema for set_status.
type SetStatusInput struct {
	ID     string `json:"id" jsonschema:"note id"`
	Status string `json:"status" jsonschema:"done to tick the note off, open to un-tick it"`
}

// SetStatus is the tool's logic.
func SetStatus(ctx context.Context, svc *cloud.Service, in SetStatusInput) (Note, error) {
	if err := validateID("id", in.ID); err != nil {
		return Note{}, err
	}
	if in.Status == "" {
		return Note{}, errors.New("status is required: open or done")
	}
	if err := validateChoice("status", in.Status, []string{notes.StatusOpen, notes.StatusDone}); err != nil {
		return Note{}, err
	}
	me, err := svc.Me(ctx)
	if err != nil {
		return Note{}, friendly(err, "update the note")
	}
	n, err := loadLive(ctx, svc, in.ID, "update the note")
	if err != nil {
		return Note{}, err
	}
	status := in.Status
	updated, err := svc.Update(ctx, n, notes.Changes{Status: &status})
	if err != nil {
		return Note{}, friendly(err, "update the note")
	}
	return noteOut(updated, me.UID), nil
}

// AddSetStatusTool registers set_status.
func AddSetStatusTool(server *mcp.Server, svc *cloud.Service) {
	tool := &mcp.Tool{
		Name:        "set_status",
		Description: "Mark a QuickNotes note as done (ticked off) or open again.",
		Annotations: writeHints("Mark done / open", true),
		InputSchema: inputSchema[SetStatusInput](func(p map[string]*jsonschema.Schema) {
			idProp(p["id"])
			p["status"].Enum = enum(notes.StatusOpen, notes.StatusDone)
		}),
	}
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in SetStatusInput) (*mcp.CallToolResult, Note, error) {
		out, err := SetStatus(ctx, svc, in)
		return nil, out, err
	})
}

// ---------- move_notes ----------

// MoveNotesInput is the argument schema for move_notes.
type MoveNotesInput struct {
	IDs    []string `json:"ids" jsonschema:"ids of the notes to move (1-50)"`
	ToDate string   `json:"to_date" jsonschema:"day to move them to, YYYY-MM-DD"`
}

// MoveNotesOutput is what move_notes and move_unfinished_to_tomorrow return.
type MoveNotesOutput struct {
	ToDate string `json:"to_date"`
	Moved  int    `json:"moved"`
	Notes  []Note `json:"notes"`
}

// MoveNotes is the tool's logic. Either every note moves or none does.
func MoveNotes(ctx context.Context, svc *cloud.Service, in MoveNotesInput) (MoveNotesOutput, error) {
	if len(in.IDs) == 0 || len(in.IDs) > 50 {
		return MoveNotesOutput{}, fmt.Errorf("ids must list 1 to 50 note ids, got %d", len(in.IDs))
	}
	seen := map[string]bool{}
	for _, id := range in.IDs {
		if err := validateID("ids", id); err != nil {
			return MoveNotesOutput{}, err
		}
		if seen[id] {
			return MoveNotesOutput{}, fmt.Errorf("ids lists %s twice", id)
		}
		seen[id] = true
	}
	if in.ToDate == "" {
		return MoveNotesOutput{}, errors.New("to_date is required (YYYY-MM-DD)")
	}
	to, err := resolveDate("to_date", in.ToDate, "")
	if err != nil {
		return MoveNotesOutput{}, err
	}
	me, err := svc.Me(ctx)
	if err != nil {
		return MoveNotesOutput{}, friendly(err, "move the notes")
	}
	var list []notes.Note
	for _, id := range in.IDs {
		n, err := loadLive(ctx, svc, id, "move the notes")
		if err != nil {
			return MoveNotesOutput{}, err
		}
		list = append(list, n)
	}
	moved, err := svc.UpdateMany(ctx, list, notes.Changes{NoteDate: &to})
	if err != nil {
		return MoveNotesOutput{}, friendly(err, "move the notes")
	}
	return MoveNotesOutput{ToDate: to, Moved: len(moved), Notes: notesOut(moved, me.UID)}, nil
}

// AddMoveNotesTool registers move_notes.
func AddMoveNotesTool(server *mcp.Server, svc *cloud.Service) {
	tool := &mcp.Tool{
		Name:        "move_notes",
		Description: "Move one or more QuickNotes notes to another day (any date). The app keeps track of how often a note was moved.",
		Annotations: writeHints("Move notes", true),
		InputSchema: inputSchema[MoveNotesInput](func(p map[string]*jsonschema.Schema) {
			p["ids"].MinItems = jsonschema.Ptr(1)
			p["ids"].MaxItems = jsonschema.Ptr(50)
			p["ids"].UniqueItems = true
			idProp(p["ids"].Items)
			dateProp(p["to_date"])
		}),
	}
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in MoveNotesInput) (*mcp.CallToolResult, MoveNotesOutput, error) {
		out, err := MoveNotes(ctx, svc, in)
		return nil, out, err
	})
}

// ---------- move_unfinished_to_tomorrow ----------

// MoveUnfinishedInput is the argument schema for move_unfinished_to_tomorrow.
type MoveUnfinishedInput struct {
	FromDate string `json:"from_date,omitempty" jsonschema:"day whose unfinished notes move to the next day, YYYY-MM-DD; default today"`
}

// MoveUnfinished is the tool's logic: every open, non-trashed note of from_date moves to the next day.
func MoveUnfinished(ctx context.Context, svc *cloud.Service, in MoveUnfinishedInput) (MoveNotesOutput, error) {
	from, err := resolveDate("from_date", in.FromDate, svc.Today())
	if err != nil {
		return MoveNotesOutput{}, err
	}
	to := notes.AddDays(from, 1)
	all, me, err := svc.All(ctx)
	if err != nil {
		return MoveNotesOutput{}, friendly(err, "load your notes")
	}
	var open []notes.Note
	for _, n := range notes.ForView(all, notes.ViewDay, from, me.UID) {
		if n.Status == notes.StatusOpen {
			open = append(open, n)
		}
	}
	if len(open) == 0 {
		return MoveNotesOutput{ToDate: to, Notes: []Note{}}, nil
	}
	moved, err := svc.UpdateMany(ctx, open, notes.Changes{NoteDate: &to})
	if err != nil {
		return MoveNotesOutput{}, friendly(err, "move the notes")
	}
	return MoveNotesOutput{ToDate: to, Moved: len(moved), Notes: notesOut(moved, me.UID)}, nil
}

// AddMoveUnfinishedTool registers move_unfinished_to_tomorrow.
func AddMoveUnfinishedTool(server *mcp.Server, svc *cloud.Service) {
	tool := &mcp.Tool{
		Name:        "move_unfinished_to_tomorrow",
		Description: "Move every unfinished QuickNotes note of a day (default today) to the following day, like the app's 'Move all to tomorrow' button.",
		Annotations: writeHints("Move unfinished to tomorrow", false),
		InputSchema: inputSchema[MoveUnfinishedInput](func(p map[string]*jsonschema.Schema) { dateProp(p["from_date"]) }),
	}
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in MoveUnfinishedInput) (*mcp.CallToolResult, MoveNotesOutput, error) {
		out, err := MoveUnfinished(ctx, svc, in)
		return nil, out, err
	})
}

// ---------- update_checklist_item ----------

// UpdateChecklistItemInput is the argument schema for update_checklist_item.
type UpdateChecklistItemInput struct {
	NoteID string `json:"note_id" jsonschema:"note id"`
	ItemID string `json:"item_id" jsonschema:"checklist item id from the note's checklist"`
	Done   *bool  `json:"done,omitempty" jsonschema:"tick (true) or un-tick (false) the item"`
	Text   string `json:"text,omitempty" jsonschema:"new wording for the item (1-500 characters)"`
}

// UpdateChecklistItem is the tool's logic: only that item changes.
func UpdateChecklistItem(ctx context.Context, svc *cloud.Service, in UpdateChecklistItemInput) (Note, error) {
	if err := validateID("note_id", in.NoteID); err != nil {
		return Note{}, err
	}
	if err := validateID("item_id", in.ItemID); err != nil {
		return Note{}, err
	}
	if in.Done == nil && in.Text == "" {
		return Note{}, errors.New("give done (true/false), text, or both")
	}
	text := ""
	if in.Text != "" {
		var err error
		if text, err = validateText("text", in.Text, notes.MaxItemText); err != nil {
			return Note{}, err
		}
	}
	me, err := svc.Me(ctx)
	if err != nil {
		return Note{}, friendly(err, "update the checklist")
	}
	n, err := loadLive(ctx, svc, in.NoteID, "update the checklist")
	if err != nil {
		return Note{}, err
	}
	items := make([]notes.ChecklistItem, len(n.Checklist))
	copy(items, n.Checklist)
	found := false
	for i := range items {
		if items[i].ID == in.ItemID {
			found = true
			if in.Done != nil {
				items[i].Done = *in.Done
			}
			if text != "" {
				items[i].Text = text
			}
		}
	}
	if !found {
		return Note{}, fmt.Errorf("note %s has no checklist item %s; get_note lists its items", in.NoteID, in.ItemID)
	}
	updated, err := svc.Update(ctx, n, notes.Changes{Checklist: &items})
	if err != nil {
		return Note{}, friendly(err, "update the checklist")
	}
	return noteOut(updated, me.UID), nil
}

// AddUpdateChecklistItemTool registers update_checklist_item.
func AddUpdateChecklistItemTool(server *mcp.Server, svc *cloud.Service) {
	tool := &mcp.Tool{
		Name:        "update_checklist_item",
		Description: "Tick, un-tick or reword one checklist item inside a QuickNotes note.",
		Annotations: writeHints("Update checklist item", true),
		InputSchema: inputSchema[UpdateChecklistItemInput](func(p map[string]*jsonschema.Schema) {
			idProp(p["note_id"])
			idProp(p["item_id"])
			lengthProp(p["text"], 1, notes.MaxItemText)
		}),
	}
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in UpdateChecklistItemInput) (*mcp.CallToolResult, Note, error) {
		out, err := UpdateChecklistItem(ctx, svc, in)
		return nil, out, err
	})
}

// ---------- trash_note ----------

// TrashNoteInput is the argument schema for trash_note.
type TrashNoteInput struct {
	ID      string `json:"id" jsonschema:"note id"`
	Restore bool   `json:"restore,omitempty" jsonschema:"true takes the note back out of the Trash"`
}

// TrashNote is the tool's logic. It only sets or clears deleted_at: the note stays restorable for
// 30 days in the app's Trash and is never deleted permanently here.
func TrashNote(ctx context.Context, svc *cloud.Service, in TrashNoteInput) (Note, error) {
	if err := validateID("id", in.ID); err != nil {
		return Note{}, err
	}
	me, err := svc.Me(ctx)
	if err != nil {
		return Note{}, friendly(err, "change the note")
	}
	n, err := svc.Get(ctx, in.ID)
	if err != nil {
		return Note{}, friendly(err, "change the note")
	}
	if n.OwnerID != me.UID {
		return Note{}, errors.New("only the owner can move a note to the Trash or restore it; this note was shared with you")
	}
	switch {
	case !in.Restore && notes.Trashed(n):
		return Note{}, fmt.Errorf("note %s is already in the Trash", in.ID)
	case in.Restore && !notes.Trashed(n):
		return Note{}, fmt.Errorf("note %s is not in the Trash", in.ID)
	}
	deleted := !in.Restore
	updated, err := svc.Update(ctx, n, notes.Changes{Deleted: &deleted})
	if err != nil {
		return Note{}, friendly(err, "change the note")
	}
	return noteOut(updated, me.UID), nil
}

// AddTrashNoteTool registers trash_note.
func AddTrashNoteTool(server *mcp.Server, svc *cloud.Service) {
	tool := &mcp.Tool{
		Name: "trash_note",
		Description: "Move one of your QuickNotes notes to the Trash (it can be restored for 30 days), or restore it with restore=true. " +
			"Only works on notes you own. Nothing is ever deleted permanently by this tool.",
		Annotations: &mcp.ToolAnnotations{Title: "Trash or restore note", DestructiveHint: jsonschema.Ptr(true), IdempotentHint: false, OpenWorldHint: jsonschema.Ptr(false)},
		InputSchema: inputSchema[TrashNoteInput](func(p map[string]*jsonschema.Schema) { idProp(p["id"]) }),
	}
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in TrashNoteInput) (*mcp.CallToolResult, Note, error) {
		out, err := TrashNote(ctx, svc, in)
		return nil, out, err
	})
}
