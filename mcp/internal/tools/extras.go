package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MuhammadAmas/QuickNotes/mcp/internal/cloud"
)

// TodayURI is the resource with today's notes.
const TodayURI = "quicknotes://today"

// AddTodayResource registers quicknotes://today: today's notes as JSON, the same shape as
// list_notes with view=day. A resource is something the user (or client) attaches as context,
// rather than something the model decides to call.
func AddTodayResource(server *mcp.Server, svc *cloud.Service) {
	server.AddResource(&mcp.Resource{
		URI:         TodayURI,
		Name:        "today",
		Title:       "Today's notes",
		Description: "Today's QuickNotes notes (yours and those shared with you), open ones first.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		out, err := ListNotes(ctx, svc, ListNotesInput{View: "day"})
		if err != nil {
			return nil, err
		}
		body, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: TodayURI, MIMEType: "application/json", Text: string(body)}}}, nil
	})
}

// AddWeeklyReviewPrompt registers weekly-review: a ready-made request the user can pick in their
// AI client to review the week with the QuickNotes tools.
func AddWeeklyReviewPrompt(server *mcp.Server) {
	server.AddPrompt(&mcp.Prompt{
		Name:        "weekly-review",
		Title:       "Weekly review",
		Description: "Review the last 7 days of QuickNotes and plan what to finish, move or drop.",
		Arguments:   []*mcp.PromptArgument{{Name: "focus", Description: "optional: a project or topic to concentrate on"}},
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		focus := strings.TrimSpace(req.Params.Arguments["focus"])
		text := "Please run my weekly review in QuickNotes.\n" +
			"1. Call weekly_summary and tell me in two or three sentences how the week went (done vs the week before, overdue, what keeps slipping).\n" +
			"2. Call list_notes with view=open and group my unfinished notes into: finish this week, move to a specific day, or drop.\n" +
			"3. Suggest the moves, but ask me before calling move_notes, set_status or trash_note."
		if focus != "" {
			text += "\nConcentrate on notes about: " + focus
		}
		return &mcp.GetPromptResult{
			Description: "Weekly review of QuickNotes",
			Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
		}, nil
	})
}
