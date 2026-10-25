package notes

import (
	"sort"
	"strings"
	"time"
)

// Views offered by list_notes, matching the web app's sidebar.
const (
	ViewDay    = "day"
	ViewOpen   = "open"
	ViewDone   = "done"
	ViewShared = "shared"
	ViewWeek   = "week"
)

const dateLayout = "2006-01-02"

// ParseDate reads a YYYY-MM-DD date; ok is false for anything else (including 2026-02-30).
func ParseDate(s string) (time.Time, bool) {
	t, err := time.Parse(dateLayout, s)
	return t, err == nil && t.Format(dateLayout) == s
}

// AddDays returns the date n days after iso (both YYYY-MM-DD).
func AddDays(iso string, n int) string {
	t, _ := time.Parse(dateLayout, iso)
	return t.AddDate(0, 0, n).Format(dateLayout)
}

// WeekStart returns the Monday of the week containing iso, like weekStart() in app.js.
func WeekStart(iso string) string {
	t, _ := time.Parse(dateLayout, iso)
	return t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7)).Format(dateLayout)
}

// Today returns now's date in loc.
func Today(now time.Time, loc *time.Location) string { return now.In(loc).Format(dateLayout) }

// LocalDay returns the date in loc of an ISO timestamp, or "" when ts is empty or invalid.
func LocalDay(ts *string, loc *time.Location) string {
	if ts == nil || *ts == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, *ts)
	if err != nil {
		return ""
	}
	return t.In(loc).Format(dateLayout)
}

// Rank is a note's priority: the order set by dragging, or when it was added if never dragged.
func Rank(n Note) float64 {
	if n.Order != nil {
		return *n.Order
	}
	if t, err := time.Parse(time.RFC3339Nano, n.CreatedAt); err == nil {
		return float64(t.UnixMilli())
	}
	return 0
}

// Trashed reports whether a note is in the Trash.
func Trashed(n Note) bool { return n.DeletedAt != nil && *n.DeletedAt != "" }

// Live drops trashed notes, like liveNotes() in app.js.
func Live(all []Note) []Note {
	out := []Note{}
	for _, n := range all {
		if !Trashed(n) {
			out = append(out, n)
		}
	}
	return out
}

func byRank(list []Note) {
	sort.SliceStable(list, func(i, j int) bool { return Rank(list[i]) < Rank(list[j]) })
}

// openThenDone sorts each status by rank and puts open notes first.
func openThenDone(list []Note) []Note {
	var open, done []Note
	for _, n := range list {
		if n.Status == StatusDone {
			done = append(done, n)
		} else {
			open = append(open, n)
		}
	}
	byRank(open)
	byRank(done)
	return append(append([]Note{}, open...), done...)
}

// ForView returns the live notes a view shows, in the web app's order:
//   - day: notes dated date, open first, each by rank;
//   - open/done/shared: grouped by date (open: oldest day first; others newest first),
//     open before done within a day;
//   - week: Monday–Sunday of date's week, day by day, open before done.
func ForView(all []Note, view, date, uid string) []Note {
	live := Live(all)
	var picked []Note
	keep := func(f func(Note) bool) {
		for _, n := range live {
			if f(n) {
				picked = append(picked, n)
			}
		}
	}
	switch view {
	case ViewOpen:
		keep(func(n Note) bool { return n.Status == StatusOpen })
	case ViewDone:
		keep(func(n Note) bool { return n.Status == StatusDone })
	case ViewShared:
		keep(func(n Note) bool { return n.OwnerID != uid || len(n.SharedWith) > 0 })
	case ViewWeek:
		start := WeekStart(date)
		end := AddDays(start, 6)
		keep(func(n Note) bool { return n.NoteDate >= start && n.NoteDate <= end })
		return groupedByDate(picked, false)
	default:
		keep(func(n Note) bool { return n.NoteDate == date })
		return openThenDone(picked)
	}
	return groupedByDate(picked, view != ViewOpen)
}

func groupedByDate(list []Note, newestFirst bool) []Note {
	groups := map[string][]Note{}
	var dates []string
	for _, n := range list {
		if _, ok := groups[n.NoteDate]; !ok {
			dates = append(dates, n.NoteDate)
		}
		groups[n.NoteDate] = append(groups[n.NoteDate], n)
	}
	sort.Strings(dates)
	if newestFirst {
		for i, j := 0, len(dates)-1; i < j; i, j = i+1, j-1 {
			dates[i], dates[j] = dates[j], dates[i]
		}
	}
	out := []Note{}
	for _, d := range dates {
		out = append(out, openThenDone(groups[d])...)
	}
	return out
}

// Search finds notes whose text, owner name or checklist contains query (case-insensitive),
// like the web app's search box, newest day first. includeTrash adds the user's own trashed notes.
func Search(all []Note, query string, includeDone, includeTrash bool, uid string) []Note {
	q := strings.ToLower(query)
	var hits []Note
	for _, n := range all {
		if Trashed(n) && !(includeTrash && n.OwnerID == uid) {
			continue
		}
		if !includeDone && n.Status == StatusDone {
			continue
		}
		match := strings.Contains(strings.ToLower(n.Content), q) || strings.Contains(strings.ToLower(n.OwnerName), q)
		for _, item := range n.Checklist {
			match = match || strings.Contains(strings.ToLower(item.Text), q)
		}
		if match {
			hits = append(hits, n)
		}
	}
	return groupedByDate(hits, true)
}
