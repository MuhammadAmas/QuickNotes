package notes

import (
	"math"
	"sort"
	"time"
)

// DayCount is one bar of the weekly chart.
type DayCount struct {
	Date  string `json:"date"`
	Added int    `json:"added"`
	Done  int    `json:"done"`
}

// Summary holds the figures of the web app's Weekly summary page.
type Summary struct {
	From              string     `json:"from"`
	To                string     `json:"to"`
	Done              int        `json:"done"`
	DonePrevious7Days int        `json:"done_previous_7_days"`
	Added             int        `json:"added"`
	AddedAndDone      int        `json:"added_and_done"`
	FinishedPercent   *int       `json:"finished_percent"` // null when nothing was added
	Overdue           int        `json:"overdue"`
	PerDay            []DayCount `json:"per_day"`
	KeepsSlipping     []Note     `json:"keeps_slipping"`     // open, moved 2+ times, most moved first (top 10)
	FinishedThisWeek  []Note     `json:"finished_this_week"` // newest first (top 15)
}

// WeeklySummary computes the 7 days ending on end (YYYY-MM-DD) exactly like renderSummary() in
// app.js. Overdue counts open notes dated before end. Day boundaries use loc.
func WeeklySummary(all []Note, end string, loc *time.Location) Summary {
	from := AddDays(end, -6)
	prevFrom := AddDays(from, -7)
	inWeek := func(d string) bool { return d != "" && d >= from && d <= end }
	inPrev := func(d string) bool { return d != "" && d >= prevFrom && d < from }
	live := Live(all)

	s := Summary{From: from, To: end, KeepsSlipping: []Note{}, FinishedThisWeek: []Note{}, PerDay: make([]DayCount, 7)}
	perDay := map[string]*DayCount{} // points into s.PerDay, which never grows after this
	for i := range s.PerDay {
		s.PerDay[i].Date = AddDays(from, i)
		perDay[s.PerDay[i].Date] = &s.PerDay[i]
	}
	for _, n := range live {
		created := LocalDay(&n.CreatedAt, loc)
		completed := LocalDay(n.CompletedAt, loc)
		if inWeek(created) {
			s.Added++
			perDay[created].Added++
			if n.Status == StatusDone {
				s.AddedAndDone++
			}
		}
		if n.Status == StatusDone && inWeek(completed) {
			s.Done++
			perDay[completed].Done++
			s.FinishedThisWeek = append(s.FinishedThisWeek, n)
		}
		if n.Status == StatusDone && inPrev(completed) {
			s.DonePrevious7Days++
		}
		if n.Status == StatusOpen && n.NoteDate < end {
			s.Overdue++
		}
		if n.Status == StatusOpen && n.TimesMoved >= 2 {
			s.KeepsSlipping = append(s.KeepsSlipping, n)
		}
	}
	if s.Added > 0 {
		pct := int(math.Round(float64(s.AddedAndDone) / float64(s.Added) * 100))
		s.FinishedPercent = &pct
	}
	sort.SliceStable(s.KeepsSlipping, func(i, j int) bool { return s.KeepsSlipping[i].TimesMoved > s.KeepsSlipping[j].TimesMoved })
	sort.SliceStable(s.FinishedThisWeek, func(i, j int) bool {
		return deref(s.FinishedThisWeek[i].CompletedAt) > deref(s.FinishedThisWeek[j].CompletedAt)
	})
	if len(s.KeepsSlipping) > 10 {
		s.KeepsSlipping = s.KeepsSlipping[:10]
	}
	if len(s.FinishedThisWeek) > 15 {
		s.FinishedThisWeek = s.FinishedThisWeek[:15]
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
