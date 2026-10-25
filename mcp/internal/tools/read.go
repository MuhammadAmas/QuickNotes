package tools

import (
	"context"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MuhammadAmas/QuickNotes/mcp/internal/cloud"
	"github.com/MuhammadAmas/QuickNotes/mcp/internal/notes"
)

var views = []string{notes.ViewDay, notes.ViewOpen, notes.ViewDone, notes.ViewShared, notes.ViewWeek}

// ---------- list_notes ----------

// ListNotesInput is the argument schema for list_notes.
type ListNotesInput struct {
	Date  string `json:"date,omitempty" jsonschema:"day to show, YYYY-MM-DD; default today (Mac's time zone). For view=week, any day of that week"`
	View  string `json:"view,omitempty" jsonschema:"day (one day, default), open (all unfinished, oldest day first), done, shared, or week (Monday-Sunday)"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum notes to return, 1-200"`
}

// ListNotesOutput is what list_notes returns.
type ListNotesOutput struct {
	View      string `json:"view"`
	Date      string `json:"date,omitempty" jsonschema:"the day shown (view=day)"`
	WeekStart string `json:"week_start,omitempty" jsonschema:"Monday of the week shown (view=week)"`
	Total     int    `json:"total" jsonschema:"notes in this view before the limit"`
	Notes     []Note `json:"notes"`
}

// ListNotes is the tool's logic, kept free of MCP types so it is easy to test.
func ListNotes(ctx context.Context, svc *cloud.Service, in ListNotesInput) (ListNotesOutput, error) {
	view := in.View
	if view == "" {
		view = notes.ViewDay
	}
	if err := validateChoice("view", view, views); err != nil {
		return ListNotesOutput{}, err
	}
	date, err := resolveDate("date", in.Date, svc.Today())
	if err != nil {
		return ListNotesOutput{}, err
	}
	limit, err := resolveLimit("limit", in.Limit, 100, 200)
	if err != nil {
		return ListNotesOutput{}, err
	}
	all, me, err := svc.All(ctx)
	if err != nil {
		return ListNotesOutput{}, friendly(err, "load your notes")
	}
	list := notes.ForView(all, view, date, me.UID)
	out := ListNotesOutput{View: view, Total: len(list)}
	switch view {
	case notes.ViewDay:
		out.Date = date
	case notes.ViewWeek:
		out.WeekStart = notes.WeekStart(date)
	}
	if len(list) > limit {
		list = list[:limit]
	}
	out.Notes = notesOut(list, me.UID)
	return out, nil
}

// AddListNotesTool registers list_notes.
func AddListNotesTool(server *mcp.Server, svc *cloud.Service) {
	tool := &mcp.Tool{
		Name: "list_notes",
		Description: "List QuickNotes notes the way the QuickNotes app shows them: one day's notes (default: today), " +
			"all unfinished notes, finished notes, shared notes, or a Monday-Sunday week. Includes notes others shared with you. " +
			"Trashed notes are never listed.",
		Annotations: &mcp.ToolAnnotations{Title: "List notes", ReadOnlyHint: true},
		InputSchema: inputSchema[ListNotesInput](func(p map[string]*jsonschema.Schema) {
			dateProp(p["date"])
			p["view"].Enum = enum(views...)
			p["view"].Default = []byte(`"day"`)
			rangeProp(p["limit"], 1, 200, 100)
		}),
	}
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in ListNotesInput) (*mcp.CallToolResult, ListNotesOutput, error) {
		out, err := ListNotes(ctx, svc, in)
		return nil, out, err
	})
}

// ---------- search_notes ----------

// SearchNotesInput is the argument schema for search_notes.
type SearchNotesInput struct {
	Query        string `json:"query" jsonschema:"text to look for in note text, checklist items and the owner's name (case-insensitive)"`
	IncludeDone  *bool  `json:"include_done,omitempty" jsonschema:"include finished notes (default true)"`
	IncludeTrash bool   `json:"include_trash,omitempty" jsonschema:"also search your own trashed notes (default false)"`
	Limit        int    `json:"limit,omitempty" jsonschema:"maximum notes to return, 1-100"`
}

// SearchNotesOutput is what search_notes returns.
type SearchNotesOutput struct {
	Query string `json:"query"`
	Total int    `json:"total" jsonschema:"matches before the limit"`
	Notes []Note `json:"notes" jsonschema:"newest day first"`
}

// SearchNotes is the tool's logic.
func SearchNotes(ctx context.Context, svc *cloud.Service, in SearchNotesInput) (SearchNotesOutput, error) {
	query, err := validateText("query", in.Query, 200)
	if err != nil {
		return SearchNotesOutput{}, err
	}
	limit, err := resolveLimit("limit", in.Limit, 25, 100)
	if err != nil {
		return SearchNotesOutput{}, err
	}
	includeDone := in.IncludeDone == nil || *in.IncludeDone
	all, me, err := svc.All(ctx)
	if err != nil {
		return SearchNotesOutput{}, friendly(err, "search your notes")
	}
	hits := notes.Search(all, query, includeDone, in.IncludeTrash, me.UID)
	out := SearchNotesOutput{Query: query, Total: len(hits)}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out.Notes = notesOut(hits, me.UID)
	return out, nil
}

// AddSearchNotesTool registers search_notes.
func AddSearchNotesTool(server *mcp.Server, svc *cloud.Service) {
	tool := &mcp.Tool{
		Name:        "search_notes",
		Description: "Search all your QuickNotes notes (and notes shared with you) for some text, across every day.",
		Annotations: &mcp.ToolAnnotations{Title: "Search notes", ReadOnlyHint: true},
		InputSchema: inputSchema[SearchNotesInput](func(p map[string]*jsonschema.Schema) {
			lengthProp(p["query"], 1, 200)
			p["include_done"].Default = []byte("true")
			rangeProp(p["limit"], 1, 100, 25)
		}),
	}
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchNotesInput) (*mcp.CallToolResult, SearchNotesOutput, error) {
		out, err := SearchNotes(ctx, svc, in)
		return nil, out, err
	})
}

// ---------- get_note ----------

// GetNoteInput is the argument schema for get_note.
type GetNoteInput struct {
	ID string `json:"id" jsonschema:"note id from list_notes or search_notes"`
}

// GetNote is the tool's logic.
func GetNote(ctx context.Context, svc *cloud.Service, in GetNoteInput) (Note, error) {
	if err := validateID("id", in.ID); err != nil {
		return Note{}, err
	}
	me, err := svc.Me(ctx)
	if err != nil {
		return Note{}, friendly(err, "load the note")
	}
	n, err := svc.Get(ctx, in.ID)
	if err != nil {
		return Note{}, friendly(err, "load the note")
	}
	return noteOut(n, me.UID), nil
}

// AddGetNoteTool registers get_note.
func AddGetNoteTool(server *mcp.Server, svc *cloud.Service) {
	tool := &mcp.Tool{
		Name:        "get_note",
		Description: "Get one QuickNotes note by id, with its checklist, sharing and move history.",
		Annotations: &mcp.ToolAnnotations{Title: "Get note", ReadOnlyHint: true},
		InputSchema: inputSchema[GetNoteInput](func(p map[string]*jsonschema.Schema) { idProp(p["id"]) }),
	}
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in GetNoteInput) (*mcp.CallToolResult, Note, error) {
		out, err := GetNote(ctx, svc, in)
		return nil, out, err
	})
}

// ---------- weekly_summary ----------

// WeeklySummaryInput is the argument schema for weekly_summary.
type WeeklySummaryInput struct {
	EndDate string `json:"end_date,omitempty" jsonschema:"last day of the 7-day period, YYYY-MM-DD; default today"`
}

// WeeklySummaryOutput is what weekly_summary returns: the web app's Weekly summary figures.
type WeeklySummaryOutput struct {
	From              string           `json:"from"`
	To                string           `json:"to"`
	Done              int              `json:"done" jsonschema:"notes finished in the period"`
	DonePrevious7Days int              `json:"done_previous_7_days"`
	Added             int              `json:"added" jsonschema:"notes created in the period"`
	AddedAndDone      int              `json:"added_and_done" jsonschema:"of those added, how many are done"`
	FinishedPercent   *int             `json:"finished_percent" jsonschema:"added_and_done as a percentage; null when nothing was added"`
	Overdue           int              `json:"overdue" jsonschema:"open notes dated before to"`
	PerDay            []notes.DayCount `json:"per_day"`
	KeepsSlipping     []Note           `json:"keeps_slipping" jsonschema:"open notes moved to another day 2+ times, most moved first"`
	FinishedThisWeek  []Note           `json:"finished_this_week" jsonschema:"newest first, up to 15"`
}

// WeeklySummary is the tool's logic.
func WeeklySummary(ctx context.Context, svc *cloud.Service, in WeeklySummaryInput) (WeeklySummaryOutput, error) {
	end, err := resolveDate("end_date", in.EndDate, svc.Today())
	if err != nil {
		return WeeklySummaryOutput{}, err
	}
	all, me, err := svc.All(ctx)
	if err != nil {
		return WeeklySummaryOutput{}, friendly(err, "load your notes")
	}
	s := notes.WeeklySummary(all, end, svc.Loc)
	return WeeklySummaryOutput{
		From: s.From, To: s.To, Done: s.Done, DonePrevious7Days: s.DonePrevious7Days, Added: s.Added,
		AddedAndDone: s.AddedAndDone, FinishedPercent: s.FinishedPercent, Overdue: s.Overdue, PerDay: s.PerDay,
		KeepsSlipping: notesOut(s.KeepsSlipping, me.UID), FinishedThisWeek: notesOut(s.FinishedThisWeek, me.UID),
	}, nil
}

// AddWeeklySummaryTool registers weekly_summary.
func AddWeeklySummaryTool(server *mcp.Server, svc *cloud.Service) {
	tool := &mcp.Tool{
		Name: "weekly_summary",
		Description: "Summarize the last 7 days in QuickNotes, like the app's Weekly summary page: notes finished (vs the previous week), " +
			"added, percentage of added notes finished, overdue notes, notes that keep slipping, and per-day counts.",
		Annotations: &mcp.ToolAnnotations{Title: "Weekly summary", ReadOnlyHint: true},
		InputSchema: inputSchema[WeeklySummaryInput](func(p map[string]*jsonschema.Schema) { dateProp(p["end_date"]) }),
	}
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in WeeklySummaryInput) (*mcp.CallToolResult, WeeklySummaryOutput, error) {
		out, err := WeeklySummary(ctx, svc, in)
		return nil, out, err
	})
}
