// Package tools defines what the MCP server offers: ten tools, the quicknotes://today resource
// and the weekly-review prompt. Each tool checks its input, calls the notes service and turns
// the result into structured MCP output.
package tools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MuhammadAmas/QuickNotes/mcp/internal/cloud"
)

// Register adds every tool, the resource and the prompt to server. They all share svc, so they
// share its sign-in, HTTP connections and clock.
//
// To add a tool: write it with an AddXxxTool function in the matching file, then call it here.
func Register(server *mcp.Server, svc *cloud.Service) {
	// read
	AddListNotesTool(server, svc)
	AddSearchNotesTool(server, svc)
	AddGetNoteTool(server, svc)
	AddWeeklySummaryTool(server, svc)
	// write
	AddAddNoteTool(server, svc)
	AddSetStatusTool(server, svc)
	AddMoveNotesTool(server, svc)
	AddMoveUnfinishedTool(server, svc)
	AddUpdateChecklistItemTool(server, svc)
	AddTrashNoteTool(server, svc)
	// extras
	AddTodayResource(server, svc)
	AddWeeklyReviewPrompt(server)
}
