package main

// End-to-end tests: a real MCP client talks to the real server over in-memory transports, with
// the fake Firebase (securetoken + Firestore, enforcing the security rules the tools rely on)
// behind it. These implement the read-journey, write-journey and input-rejection Oracles.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MuhammadAmas/QuickNotes/mcp/internal/auth"
	"github.com/MuhammadAmas/QuickNotes/mcp/internal/fakefirebase"
	"github.com/MuhammadAmas/QuickNotes/mcp/internal/firestore"
)

const (
	canaryRefresh = "CANARY-REFRESH-TOKEN-3141"
	canaryID      = "CANARY-ID-TOKEN-2718"
	project       = "test-project"
)

var (
	owner  = fakefirebase.Account{UID: "u-owner", Email: "owner@example.com", Name: "Owner Name", RefreshToken: canaryRefresh}
	friend = fakefirebase.Account{UID: "u-friend", Email: "friend@example.com", Name: "Friend Name"}
	// 09:00 on Friday 17 October 2026 in a fixed +05:00 zone (the Mac's "local" time in these tests)
	zone = time.FixedZone("PKT", 5*3600)
	now  = time.Date(2026, 10, 17, 9, 0, 0, 0, zone)
)

func init() {
	owner.IDToken = fakefirebase.IDTokenFor(owner, canaryID)
	friend.IDToken = fakefirebase.IDTokenFor(friend, "friend-token")
}

type env struct {
	fake    *fakefirebase.Fake
	session *mcp.ClientSession
	macFile string
}

// seed puts a realistic mix of notes into the fake.
func seed(f *fakefirebase.Fake) {
	note := func(id, ownerUID, ownerEmail, ownerName, content, date, status string, extra map[string]any) {
		fields := map[string]any{
			"ownerId": ownerUID, "ownerEmail": ownerEmail, "ownerName": ownerName, "content": content, "note_date": date,
			"status": status, "created_at": "2026-10-15T05:00:00.000Z", "updated_at": "2026-10-15T05:00:00.000Z",
			"edited_at": nil, "completed_at": nil, "original_date": nil, "times_moved": 0, "sharedWith": []any{},
		}
		for k, v := range extra {
			fields[k] = v
		}
		f.Put("qn_notes", id, fields)
	}
	o := func(id, content, date, status string, extra map[string]any) {
		note(id, owner.UID, owner.Email, owner.Name, content, date, status, extra)
	}
	o("today-open", "Call Sam about the budget", "2026-10-17", "open", map[string]any{"order": 1000})
	o("today-first", "Send the Q4 draft", "2026-10-17", "open", map[string]any{"order": 500, "sharedWith": []any{"priya@example.com"}})
	o("today-done", "Standup notes", "2026-10-17", "done", map[string]any{"order": 100, "completed_at": "2026-10-17T03:30:00.000Z"})
	o("yesterday-open", "Write Q4 OKRs", "2026-10-16", "open", map[string]any{"order": 50, "times_moved": 2, "original_date": "2026-10-14"})
	o("trashed", "Old idea", "2026-10-17", "open", map[string]any{"order": 10, "deleted_at": "2026-10-16T10:00:00.000Z"})
	o("tomorrow-list", "Trip", "2026-10-18", "open", map[string]any{"order": 70, "checklist": []any{
		map[string]any{"id": "bags", "text": "Pack bags", "done": false},
		map[string]any{"id": "tix", "text": "Print tickets", "done": true},
	}})
	o("last-week-done", "Fix login bug", "2026-10-11", "done", map[string]any{"order": 5, "completed_at": "2026-10-11T08:00:00.000Z"})
	note("shared-in", friend.UID, friend.Email, friend.Name, "Vendor contract: review indemnity clause", "2026-10-17", "open",
		map[string]any{"order": 2000, "sharedWith": []any{owner.Email}})
	note("friend-private", friend.UID, friend.Email, friend.Name, "Friend's private note", "2026-10-17", "open", map[string]any{"order": 1})
}

// connect starts the real server (with the fake Firebase behind it) and an MCP client, joined by
// in-memory transports.
func connect(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	fake := fakefirebase.New(project, owner, friend)
	t.Cleanup(fake.Close)
	seed(fake)

	dir := t.TempDir()
	mac := filepath.Join(dir, "session.json")
	b, _ := json.Marshal(map[string]string{"refreshToken": canaryRefresh, "uid": owner.UID, "email": owner.Email, "name": owner.Name})
	if err := os.WriteFile(mac, b, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := auth.Config{APIKey: "test-key", ProjectID: project}
	tokens := auth.NewTokenSource(cfg, []auth.SessionFile{{Path: mac, Label: "Mac app session"}}, auth.Options{Endpoint: fake.TokenURL()})
	svc := newService(cfg, tokens, firestore.Options{BaseURL: fake.Server.URL})
	svc.Now = func() time.Time { return now }
	svc.Loc = zone

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := newServer(svc).Connect(ctx, serverTransport, nil) // server first, then client
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return &env{fake: fake, session: session, macFile: mac}
}

func resultText(res *mcp.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	if text, ok := res.Content[0].(*mcp.TextContent); ok {
		return text.Text
	}
	return ""
}

// call runs a tool that must succeed and decodes its structured content into out.
func (e *env) call(t *testing.T, tool string, args map[string]any, out any) {
	t.Helper()
	res, err := e.session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error %v", tool, err)
	}
	if res.IsError {
		t.Fatalf("%s: tool error: %s", tool, resultText(res))
	}
	data, _ := json.Marshal(res.StructuredContent)
	reflect.ValueOf(out).Elem().SetZero() // decode into a clean value, never on top of an earlier answer
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("%s: decoding %s: %v", tool, data, err)
	}
}

// fails runs a tool that must return a tool error and returns its text.
func (e *env) fails(t *testing.T, tool string, args map[string]any) string {
	t.Helper()
	res, err := e.session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error, want a tool error: %v", tool, err)
	}
	if !res.IsError {
		t.Fatalf("%s %v: IsError = false, want true", tool, args)
	}
	return resultText(res)
}

type noteOut struct {
	ID           string `json:"id"`
	Content      string `json:"content"`
	Date         string `json:"date"`
	Status       string `json:"status"`
	Mine         bool   `json:"mine"`
	Owner        string `json:"owner"`
	TimesMoved   int    `json:"times_moved"`
	OriginalDate string `json:"original_date"`
	CompletedAt  string `json:"completed_at"`
	InTrash      bool   `json:"in_trash"`
	Checklist    []struct {
		ID   string `json:"id"`
		Text string `json:"text"`
		Done bool   `json:"done"`
	} `json:"checklist"`
}

type listOut struct {
	View      string    `json:"view"`
	Date      string    `json:"date"`
	WeekStart string    `json:"week_start"`
	Total     int       `json:"total"`
	Notes     []noteOut `json:"notes"`
}

func ids(list []noteOut) []string {
	out := []string{}
	for _, n := range list {
		out = append(out, n.ID)
	}
	return out
}

func TestListsToolsResourceAndPrompt(t *testing.T) {
	e := connect(t)
	ctx := context.Background()
	res, err := e.session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	readOnly := []string{"get_note", "list_notes", "search_notes", "weekly_summary"}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" || tool.Annotations == nil || tool.OutputSchema == nil {
			t.Errorf("%s: missing description, annotations or output schema", tool.Name)
			continue
		}
		if got := tool.Annotations.ReadOnlyHint; got != slices.Contains(readOnly, tool.Name) {
			t.Errorf("%s: readOnlyHint = %v", tool.Name, got)
		}
		destructive := tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint
		if !tool.Annotations.ReadOnlyHint && destructive != (tool.Name == "trash_note") {
			t.Errorf("%s: destructiveHint = %v", tool.Name, destructive)
		}
	}
	slices.Sort(names)
	want := []string{"add_note", "get_note", "list_notes", "move_notes", "move_unfinished_to_tomorrow", "search_notes",
		"set_status", "trash_note", "update_checklist_item", "weekly_summary"}
	if !slices.Equal(names, want) {
		t.Errorf("tools = %v\nwant    %v", names, want)
	}
	resources, err := e.session.ListResources(ctx, nil)
	if err != nil || len(resources.Resources) != 1 || resources.Resources[0].URI != "quicknotes://today" {
		t.Errorf("resources = %+v, %v", resources, err)
	}
	prompts, err := e.session.ListPrompts(ctx, nil)
	if err != nil || len(prompts.Prompts) != 1 || prompts.Prompts[0].Name != "weekly-review" {
		t.Errorf("prompts = %+v, %v", prompts, err)
	}
}

// Oracle: the AI reads notes end to end.
func TestReadJourney(t *testing.T) {
	e := connect(t)
	var day listOut
	e.call(t, "list_notes", map[string]any{}, &day)
	// open by drag order (yours and shared), then done; never trashed or other people's private notes
	if want := []string{"today-first", "today-open", "shared-in", "today-done"}; !reflect.DeepEqual(ids(day.Notes), want) || day.Date != "2026-10-17" {
		t.Errorf("today = %v (date %s), want %v", ids(day.Notes), day.Date, want)
	}
	shared := day.Notes[2]
	if shared.Mine || shared.Owner != "Friend Name" {
		t.Errorf("shared note should show its owner: %+v", shared)
	}

	var open listOut
	e.call(t, "list_notes", map[string]any{"view": "open"}, &open)
	if want := []string{"yesterday-open", "today-first", "today-open", "shared-in", "tomorrow-list"}; !reflect.DeepEqual(ids(open.Notes), want) {
		t.Errorf("open = %v, want %v (oldest day first)", ids(open.Notes), want)
	}
	var done listOut
	e.call(t, "list_notes", map[string]any{"view": "done"}, &done)
	if want := []string{"today-done", "last-week-done"}; !reflect.DeepEqual(ids(done.Notes), want) {
		t.Errorf("done = %v, want %v", ids(done.Notes), want)
	}
	var sharedView listOut
	e.call(t, "list_notes", map[string]any{"view": "shared"}, &sharedView)
	if want := []string{"today-first", "shared-in"}; !reflect.DeepEqual(ids(sharedView.Notes), want) {
		t.Errorf("shared = %v, want %v", ids(sharedView.Notes), want)
	}
	var week listOut
	e.call(t, "list_notes", map[string]any{"view": "week", "date": "2026-10-15"}, &week)
	if week.WeekStart != "2026-10-12" || !reflect.DeepEqual(ids(week.Notes), []string{"yesterday-open", "today-first", "today-open", "shared-in", "today-done", "tomorrow-list"}) {
		t.Errorf("week from %s = %v", week.WeekStart, ids(week.Notes))
	}
	var limited listOut
	e.call(t, "list_notes", map[string]any{"view": "open", "limit": 2}, &limited)
	if limited.Total != 5 || len(limited.Notes) != 2 {
		t.Errorf("limit: total %d, got %d", limited.Total, len(limited.Notes))
	}

	var search struct {
		Total int       `json:"total"`
		Notes []noteOut `json:"notes"`
	}
	e.call(t, "search_notes", map[string]any{"query": "INDEMNITY"}, &search)
	if !reflect.DeepEqual(ids(search.Notes), []string{"shared-in"}) {
		t.Errorf("search indemnity = %v", ids(search.Notes))
	}
	e.call(t, "search_notes", map[string]any{"query": "tickets"}, &search) // checklist text
	if !reflect.DeepEqual(ids(search.Notes), []string{"tomorrow-list"}) {
		t.Errorf("search checklist = %v", ids(search.Notes))
	}
	e.call(t, "search_notes", map[string]any{"query": "old idea"}, &search)
	if search.Total != 0 {
		t.Errorf("trashed notes must not be found by default: %v", ids(search.Notes))
	}
	e.call(t, "search_notes", map[string]any{"query": "old idea", "include_trash": true}, &search)
	if !reflect.DeepEqual(ids(search.Notes), []string{"trashed"}) || !search.Notes[0].InTrash {
		t.Errorf("include_trash = %+v", search.Notes)
	}
	e.call(t, "search_notes", map[string]any{"query": "q4", "include_done": false}, &search)
	if !reflect.DeepEqual(ids(search.Notes), []string{"today-first", "yesterday-open"}) {
		t.Errorf("search q4 = %v", ids(search.Notes))
	}

	var got noteOut
	e.call(t, "get_note", map[string]any{"id": "yesterday-open"}, &got)
	if got.TimesMoved != 2 || got.OriginalDate != "2026-10-14" {
		t.Errorf("get_note = %+v", got)
	}
	if msg := e.fails(t, "get_note", map[string]any{"id": "friend-private"}); !strings.Contains(msg, "not found") {
		t.Errorf("someone else's private note must not be readable: %s", msg)
	}

	var summary struct {
		From, To        string
		Done            int  `json:"done"`
		DonePrev        int  `json:"done_previous_7_days"`
		Added           int  `json:"added"`
		AddedAndDone    int  `json:"added_and_done"`
		FinishedPercent *int `json:"finished_percent"`
		Overdue         int  `json:"overdue"`
		PerDay          []struct {
			Date        string
			Added, Done int
		} `json:"per_day"`
		KeepsSlipping []noteOut `json:"keeps_slipping"`
	}
	e.call(t, "weekly_summary", map[string]any{}, &summary)
	// added: the 7 visible live notes were all created 2026-10-15; done this week: today-done (17th, +05:00) and
	// last-week-done (11th); 2 of 7 added are done = 29%; overdue: yesterday-open
	perDay := map[string][2]int{}
	for _, d := range summary.PerDay {
		perDay[d.Date] = [2]int{d.Added, d.Done}
	}
	if summary.From != "2026-10-11" || summary.To != "2026-10-17" || summary.Added != 7 || summary.Done != 2 || summary.AddedAndDone != 2 ||
		summary.FinishedPercent == nil || *summary.FinishedPercent != 29 || summary.Overdue != 1 || len(summary.PerDay) != 7 ||
		perDay["2026-10-15"] != [2]int{7, 0} || perDay["2026-10-11"] != [2]int{0, 1} || perDay["2026-10-17"] != [2]int{0, 1} ||
		!reflect.DeepEqual(ids(summary.KeepsSlipping), []string{"yesterday-open"}) {
		t.Errorf("summary = %+v", summary)
	}

	ctx := context.Background()
	res, err := e.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "quicknotes://today"})
	if err != nil || len(res.Contents) != 1 || res.Contents[0].MIMEType != "application/json" {
		t.Fatalf("read resource: %+v %v", res, err)
	}
	var today listOut
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &today); err != nil || !reflect.DeepEqual(ids(today.Notes), ids(day.Notes)) {
		t.Errorf("resource = %s (%v)", res.Contents[0].Text, err)
	}
	prompt, err := e.session.GetPrompt(ctx, &mcp.GetPromptParams{Name: "weekly-review", Arguments: map[string]string{"focus": "budget"}})
	if err != nil || len(prompt.Messages) != 1 {
		t.Fatalf("prompt: %+v %v", prompt, err)
	}
	text := prompt.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(text, "weekly_summary") || !strings.Contains(text, "budget") || !strings.Contains(text, "ask me before") {
		t.Errorf("prompt text = %q", text)
	}
}

// commits returns the decoded bodies of every :commit request so far.
func commits(f *fakefirebase.Fake) []map[string]any {
	var out []map[string]any
	for _, r := range f.Requests() {
		if strings.HasSuffix(r.Path, ":commit") {
			var body map[string]any
			_ = json.Unmarshal([]byte(r.Body), &body)
			out = append(out, body)
		}
	}
	return out
}

func lastMask(t *testing.T, f *fakefirebase.Fake) []string {
	t.Helper()
	all := commits(f)
	if len(all) == 0 {
		t.Fatal("no commit was sent")
	}
	w := all[len(all)-1]["writes"].([]any)[0].(map[string]any)
	var mask []string
	for _, p := range w["updateMask"].(map[string]any)["fieldPaths"].([]any) {
		mask = append(mask, p.(string))
	}
	return mask
}

// Oracle: the AI changes notes end to end, writing exactly the fields the apps write.
func TestWriteJourney(t *testing.T) {
	e := connect(t)
	untouched := e.fake.Doc("qn_notes", "friend-private")

	var added noteOut
	e.call(t, "add_note", map[string]any{"content": "Trip prep\n- bags\n[x] tickets"}, &added)
	doc := e.fake.Doc("qn_notes", added.ID)
	if doc == nil || doc["content"] != "Trip prep" || doc["note_date"] != "2026-10-17" || doc["ownerId"] != owner.UID ||
		doc["ownerEmail"] != owner.Email || doc["status"] != "open" || doc["created_at"] != "2026-10-17T04:00:00.000Z" {
		t.Fatalf("created doc = %v", doc)
	}
	if items := doc["checklist"].([]any); len(items) != 2 || items[1].(map[string]any)["done"] != true {
		t.Errorf("checklist = %v", items)
	}
	if user := e.fake.Doc("users", owner.UID); user == nil || user["notesCreated"] != int64(1) {
		t.Errorf("notesCreated = %v", user)
	}

	var n noteOut
	e.call(t, "set_status", map[string]any{"id": "today-open", "status": "done"}, &n)
	if n.Status != "done" || e.fake.Doc("qn_notes", "today-open")["completed_at"] != "2026-10-17T04:00:00.000Z" {
		t.Errorf("done: %+v / %v", n, e.fake.Doc("qn_notes", "today-open"))
	}
	if mask := lastMask(t, e.fake); !slices.Equal(mask, []string{"completed_at", "status", "updated_at"}) {
		t.Errorf("set_status mask = %v", mask)
	}
	e.call(t, "set_status", map[string]any{"id": "today-open", "status": "open"}, &n)
	if e.fake.Doc("qn_notes", "today-open")["completed_at"] != nil {
		t.Error("re-opening must clear completed_at")
	}

	e.call(t, "update_checklist_item", map[string]any{"note_id": "tomorrow-list", "item_id": "bags", "done": true}, &n)
	items := e.fake.Doc("qn_notes", "tomorrow-list")["checklist"].([]any)
	if items[0].(map[string]any)["done"] != true || items[1].(map[string]any)["text"] != "Print tickets" {
		t.Errorf("checklist after tick = %v", items)
	}
	if mask := lastMask(t, e.fake); !slices.Equal(mask, []string{"checklist", "updated_at"}) {
		t.Errorf("checklist mask = %v", mask)
	}

	var moved struct {
		ToDate string    `json:"to_date"`
		Moved  int       `json:"moved"`
		Notes  []noteOut `json:"notes"`
	}
	e.call(t, "move_notes", map[string]any{"ids": []string{"tomorrow-list"}, "to_date": "2026-10-20"}, &moved)
	d := e.fake.Doc("qn_notes", "tomorrow-list")
	if d["note_date"] != "2026-10-20" || d["original_date"] != "2026-10-18" || d["times_moved"] != int64(1) {
		t.Errorf("moved doc = %v", d)
	}
	if mask := lastMask(t, e.fake); !slices.Equal(mask, []string{"note_date", "original_date", "times_moved", "updated_at"}) {
		t.Errorf("move mask = %v", mask)
	}

	e.call(t, "move_unfinished_to_tomorrow", map[string]any{}, &moved)
	// every open note of today moves (yours, the one just added and the one shared with you); done ones stay
	if moved.ToDate != "2026-10-18" || !reflect.DeepEqual(ids(moved.Notes), []string{"today-first", "today-open", "shared-in", added.ID}) {
		t.Errorf("move unfinished = %+v", moved)
	}
	if e.fake.Doc("qn_notes", "today-done")["note_date"] != "2026-10-17" || e.fake.Doc("qn_notes", "shared-in")["note_date"] != "2026-10-18" {
		t.Error("done notes stay, shared open notes move")
	}

	e.call(t, "trash_note", map[string]any{"id": added.ID}, &n)
	if !n.InTrash || e.fake.Doc("qn_notes", added.ID)["deleted_at"] != "2026-10-17T04:00:00.000Z" {
		t.Errorf("trash: %+v", n)
	}
	var day listOut
	e.call(t, "list_notes", map[string]any{"date": "2026-10-17"}, &day)
	if slices.Contains(ids(day.Notes), added.ID) {
		t.Error("trashed note still listed")
	}
	e.call(t, "trash_note", map[string]any{"id": added.ID, "restore": true}, &n)
	if n.InTrash || e.fake.Doc("qn_notes", added.ID)["deleted_at"] != nil {
		t.Errorf("restore: %+v", n)
	}

	// negative: trashing a note someone shared with you is refused before any write
	before := len(commits(e.fake))
	if msg := e.fails(t, "trash_note", map[string]any{"id": "shared-in"}); !strings.Contains(msg, "only the owner") {
		t.Errorf("trash shared = %s", msg)
	}
	if len(commits(e.fake)) != before {
		t.Error("refused trash must not send a write")
	}
	if !reflect.DeepEqual(e.fake.Doc("qn_notes", "friend-private"), untouched) {
		t.Error("an unrelated note changed")
	}
	if ids := e.fake.Docs("qn_notes"); len(ids) != 10 {
		t.Errorf("no note may be deleted: %d notes left", len(ids))
	}
}

// Oracle: bad input is refused with a clear message and no Firestore call.
func TestInputRejection(t *testing.T) {
	e := connect(t)
	long := strings.Repeat("x", 10001)
	cases := []struct {
		name, tool string
		args       map[string]any
		want       string
	}{
		{"impossible date", "list_notes", map[string]any{"date": "2026-02-30"}, "real date"},
		{"date pattern", "list_notes", map[string]any{"date": "17/10/2026"}, "date"},
		{"unknown view", "list_notes", map[string]any{"view": "month"}, "view"},
		{"limit too big", "list_notes", map[string]any{"limit": 500}, "limit"},
		{"limit wrong type", "list_notes", map[string]any{"limit": "ten"}, "limit"},
		{"empty query", "search_notes", map[string]any{"query": "   "}, "query"},
		{"missing query", "search_notes", map[string]any{}, "query"},
		{"bad id", "get_note", map[string]any{"id": "../users/u-owner"}, "id"},
		{"empty content", "add_note", map[string]any{"content": " \n "}, "content"},
		{"content too long", "add_note", map[string]any{"content": long}, "content"},
		{"bad status", "set_status", map[string]any{"id": "today-open", "status": "finished"}, "status"},
		{"no ids", "move_notes", map[string]any{"ids": []string{}, "to_date": "2026-10-20"}, "ids"},
		{"too many ids", "move_notes", map[string]any{"ids": slices.Repeat([]string{"a"}, 51), "to_date": "2026-10-20"}, "ids"},
		{"missing to_date", "move_notes", map[string]any{"ids": []string{"today-open"}}, "to_date"},
		{"nothing to change", "update_checklist_item", map[string]any{"note_id": "tomorrow-list", "item_id": "bags"}, "done"},
		{"item text too long", "update_checklist_item", map[string]any{"note_id": "tomorrow-list", "item_id": "bags", "text": strings.Repeat("y", 501)}, "text"},
		{"bad from_date", "move_unfinished_to_tomorrow", map[string]any{"from_date": "tomorrow"}, "from_date"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := e.fake.FirestoreRequests()
			if msg := e.fails(t, tc.tool, tc.args); !strings.Contains(msg, tc.want) {
				t.Errorf("message %q does not mention %q", msg, tc.want)
			}
			if after := e.fake.FirestoreRequests(); after != before {
				t.Errorf("input error caused %d Firestore requests", after-before)
			}
		})
	}
	// errors that need a lookup still explain themselves and change nothing
	for _, tc := range []struct {
		tool, want string
		args       map[string]any
	}{
		{"get_note", "not found", map[string]any{"id": "no-such-note"}},
		{"update_checklist_item", "no checklist item", map[string]any{"note_id": "tomorrow-list", "item_id": "nope", "done": true}},
		{"set_status", "in the Trash", map[string]any{"id": "trashed", "status": "done"}},
		{"trash_note", "already in the Trash", map[string]any{"id": "trashed"}},
		{"trash_note", "not in the Trash", map[string]any{"id": "today-open", "restore": true}},
		{"move_notes", "not found", map[string]any{"ids": []string{"today-open", "no-such-note"}, "to_date": "2026-10-20"}},
	} {
		before := len(commits(e.fake))
		if msg := e.fails(t, tc.tool, tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s %v: %q does not mention %q", tc.tool, tc.args, msg, tc.want)
		}
		if len(commits(e.fake)) != before {
			t.Errorf("%s %v: wrote despite the error", tc.tool, tc.args)
		}
	}
	if e.fake.Doc("qn_notes", "today-open")["note_date"] != "2026-10-17" {
		t.Error("a failed move_notes must move nothing")
	}
}
