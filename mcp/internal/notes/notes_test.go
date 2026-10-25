package notes

import (
	"strings"
	"testing"
	"time"
)

func TestTruncateUTF16KeepsEmojiWhole(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 3, "hel"},
		{"ab🍎cd", 3, "ab"}, // the apple is 2 UTF-16 units; cutting through it would break it
		{"ab🍎cd", 4, "ab🍎"},
		{"café", 4, "café"},
		{"", 5, ""},
	}
	for _, c := range cases {
		if got := TruncateUTF16(c.in, c.n); got != c.want {
			t.Errorf("TruncateUTF16(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
	if LenUTF16("ab🍎") != 4 {
		t.Errorf("LenUTF16 should count the emoji as 2 units like JavaScript")
	}
}

func TestApplyShowsTheEditedNote(t *testing.T) {
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	note := Note{Content: "a", Status: StatusOpen, NoteDate: "2026-10-17"}
	done, date := StatusDone, "2026-10-18"
	got := Apply(note, PatchFor(note, Changes{Status: &done, NoteDate: &date}, now))
	if got.Status != StatusDone || got.CompletedAt == nil || got.NoteDate != date || got.TimesMoved != 1 ||
		got.OriginalDate == nil || *got.OriginalDate != "2026-10-17" || got.UpdatedAt != "2026-10-17T09:00:00.000Z" {
		t.Errorf("Apply gave %+v", got)
	}
	restored := false
	trashed := Apply(note, PatchFor(note, Changes{Deleted: &restored}, now))
	if trashed.DeletedAt != nil {
		t.Errorf("restore should clear deleted_at, got %v", *trashed.DeletedAt)
	}
}

func TestNewItemIDShape(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		id := NewItemID()
		if len(id) != 8 || seen[id] {
			t.Fatalf("bad or repeated id %q", id)
		}
		seen[id] = true
	}
}

func TestNeverDraggedNotesSortByCreationTime(t *testing.T) {
	dragged := 1760000000000.0 // between the two creation times below
	list := []Note{
		{ID: "late", Status: StatusOpen, NoteDate: "2026-10-17", CreatedAt: "2026-10-17T09:00:00.000Z"},
		{ID: "dragged", Status: StatusOpen, NoteDate: "2026-10-17", CreatedAt: "2026-10-17T10:00:00.000Z", Order: &dragged},
		{ID: "early", Status: StatusOpen, NoteDate: "2026-10-17", CreatedAt: "2025-01-01T00:00:00.000Z"},
		{ID: "broken", Status: StatusOpen, NoteDate: "2026-10-17", CreatedAt: "not a time"},
	}
	var got []string
	for _, n := range ForView(list, ViewDay, "2026-10-17", "") {
		got = append(got, n.ID)
	}
	want := []string{"broken", "early", "dragged", "late"} // unknown time ranks first (0), like the web app
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
}
