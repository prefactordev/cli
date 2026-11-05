package human

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/prefactordev/cli/internal/jsjson"
)

// column is one table column: the field it shows and how much it matters
// when the terminal is too narrow for all of them. Lower ranks are kept.
type column struct {
	key   string
	rank  int
	title string
}

// classifierKeys are short fields that tell rows apart.
var classifierKeys = []string{"role", "purpose", "execution_mode", "trigger", "token_scope", "integration_type", "provider", "content_type", "channel", "risk_score"}

// timeKeys are the time to show, in order of preference.
var timeKeys = []string{"started_at", "raised_at", "last_activity_span_at", "inserted_at", "created_at", "updated_at"}

// spans are the start and end fields a timeline can be drawn from.
var spans = [][2]string{{"started_at", "finished_at"}, {"raised_at", "cleared_at"}}

const (
	// durationKey is the column a row's duration is shown in.
	durationKey   = "\x00duration"
	timelineWidth = 24
	// timelineRank places the timeline between related IDs and times.
	timelineRank = 4
)

// list renders an array: a table for objects, one value a line otherwise.
func (r renderer) list(rows []any) string {
	if len(rows) == 0 {
		noun := r.o.Noun
		if noun == "" {
			noun = "results"
		}
		return r.t.Muted.Render("No " + noun + " found.")
	}
	objects := make([]*jsjson.Object, 0, len(rows))
	for _, row := range rows {
		o, ok := row.(*jsjson.Object)
		if !ok {
			return r.joinedLines(rows)
		}
		objects = append(objects, o)
	}

	columns := dropUninformative(chooseColumns(objects), objects)
	timeline := chooseTimeline(objects)
	if timeline != nil {
		// A duration goes before the ID, which is last.
		at := max(len(columns)-1, 0)
		columns = slices.Insert(columns, at, column{key: durationKey, rank: 2, title: "DURATION"})
	}
	cells := make([][]string, len(objects))
	for i, o := range objects {
		for _, c := range columns {
			if c.key == durationKey {
				cells[i] = append(cells[i], r.duration(o, timeline))
				continue
			}
			v, _ := o.Get(c.key)
			cells[i] = append(cells[i], r.cell(c.key, v))
		}
	}

	// Drop the least useful columns until the table fits: related IDs go
	// before the timeline, which goes before the time and classifiers.
	widthOf := func(cols []column, withTimeline bool) int {
		total := 0
		for ci, c := range cols {
			w := lipgloss.Width(c.title)
			for _, row := range cells {
				w = max(w, lipgloss.Width(row[indexOf(columns, c)]))
			}
			_ = ci
			total += w + 2
		}
		if withTimeline {
			total += timelineWidth + 2
		}
		return total
	}
	shown := columns
	showTimeline := timeline != nil
	for widthOf(shown, showTimeline) > r.o.Width {
		worst := -1
		for i, c := range shown {
			if c.rank > 0 && (worst < 0 || c.rank > shown[worst].rank) {
				worst = i
			}
		}
		if showTimeline && (worst < 0 || shown[worst].rank <= timelineRank) {
			showTimeline = false
			continue
		}
		if worst < 0 {
			break
		}
		shown = slices.Delete(slices.Clone(shown), worst, worst+1)
	}

	t := r.newTable()
	var headers []string
	for _, c := range shown {
		headers = append(headers, c.title)
	}
	if showTimeline {
		headers = append(headers, "TIMELINE")
	}
	t.Headers(headers...)
	bounds := timelineBounds(objects, timeline, r.o.Now)
	for i, o := range objects {
		var row []string
		for _, c := range shown {
			row = append(row, cells[i][indexOf(columns, c)])
		}
		if showTimeline {
			row = append(row, r.timelineBar(o, timeline, bounds))
		}
		t.Row(row...)
	}
	t.StyleFunc(func(row, _ int) lipgloss.Style {
		s := lipgloss.NewStyle().Padding(0, 1)
		if row == -1 {
			return s.Inherit(r.t.Muted).Bold(true)
		}
		return s
	})
	// A table wider than the terminal shrinks its widest cells.
	if widthOf(shown, showTimeline) > r.o.Width {
		t.Width(r.o.Width)
	}
	return trimLines(t.Render())
}

func indexOf(columns []column, c column) int {
	for i, col := range columns {
		if col.key == c.key {
			return i
		}
	}
	return -1
}

func (r renderer) joinedLines(rows []any) string {
	lines := make([]string, len(rows))
	for i, v := range rows {
		lines[i] = r.value(v, r.o.Width)
	}
	return strings.Join(lines, "\n")
}

// cell renders one table cell: short times, coloured statuses, and nothing
// for nested values.
func (r renderer) cell(key string, v any) string {
	switch v.(type) {
	case *jsjson.Object, []any:
		return r.t.Muted.Render("…")
	}
	return r.scalar(key, v, true)
}

// chooseColumns picks the fields worth a column, from the keys the rows
// have: the name, status, classifiers, related IDs, a time and the ID.
func chooseColumns(rows []*jsjson.Object) []column {
	present := map[string]bool{}
	var order []string
	for _, o := range rows {
		for _, key := range o.Keys() {
			v, _ := o.Get(key)
			switch v.(type) {
			case *jsjson.Object, []any:
				continue
			}
			if !present[key] {
				present[key] = true
				order = append(order, key)
			}
		}
	}

	var columns []column
	add := func(key string, rank int) {
		if present[key] && indexOf(columns, column{key: key}) < 0 {
			columns = append(columns, column{key: key, rank: rank, title: strings.ToUpper(humanKey(key))})
		}
	}
	for _, key := range labelKeys {
		if present[key] {
			add(key, 0)
			break
		}
	}
	for _, key := range statusKeys {
		add(key, 1)
	}
	add("summary", 2)
	for _, key := range classifierKeys {
		add(key, 3)
	}
	for _, key := range order {
		if strings.HasSuffix(key, "_id") && key != "account_id" {
			add(key, 5)
		}
	}
	for _, key := range timeKeys {
		if present[key] {
			add(key, 4)
			break
		}
	}
	add("id", 0)
	if len(columns) == 1 {
		// Nothing recognised: show the scalar fields as they come.
		columns = nil
		for i, key := range order {
			add(key, i)
		}
	}
	return columns
}

// chooseTimeline returns the start and end fields to draw a timeline from,
// when the rows have them.
func chooseTimeline(rows []*jsjson.Object) *[2]string {
	for _, pair := range spans {
		for _, o := range rows {
			if _, hasStart := o.Get(pair[0]); hasStart {
				if _, hasEnd := o.Get(pair[1]); hasEnd {
					return &pair
				}
			}
		}
	}
	return nil
}

// timelineBounds is the earliest start and latest end across the rows; a
// row without an end is still running.
func timelineBounds(rows []*jsjson.Object, pair *[2]string, now time.Time) [2]time.Time {
	var bounds [2]time.Time
	if pair == nil {
		return bounds
	}
	for _, o := range rows {
		start, end, ok := rowSpan(o, pair, now)
		if !ok {
			continue
		}
		if bounds[0].IsZero() || start.Before(bounds[0]) {
			bounds[0] = start
		}
		if end.After(bounds[1]) {
			bounds[1] = end
		}
	}
	return bounds
}

func rowSpan(o *jsjson.Object, pair *[2]string, now time.Time) (time.Time, time.Time, bool) {
	startText, _ := getString(o, pair[0])
	start, ok := parseTime(startText)
	if !ok {
		return start, start, false
	}
	end := now
	if endText, _ := getString(o, pair[1]); endText != "" {
		if parsed, ok := parseTime(endText); ok {
			end = parsed
		}
	}
	if end.Before(start) {
		end = start
	}
	return start, end, true
}

// timelineBar places a row's span within the bounds, as a bar coloured by
// its status.
func (r renderer) timelineBar(o *jsjson.Object, pair *[2]string, bounds [2]time.Time) string {
	start, end, ok := rowSpan(o, pair, r.o.Now)
	total := bounds[1].Sub(bounds[0])
	if !ok || total <= 0 {
		return r.t.BarTrack.Render(strings.Repeat("·", timelineWidth))
	}
	from := int(float64(start.Sub(bounds[0])) / float64(total) * timelineWidth)
	to := int(float64(end.Sub(bounds[0])) / float64(total) * timelineWidth)
	from = min(from, timelineWidth-1)
	to = max(min(to, timelineWidth), from+1)

	bar := r.t.Bar
	if status, ok := getString(o, "status"); ok {
		if c := statusColour(status); c != nil {
			bar = lipgloss.NewStyle().Foreground(c)
		}
	}
	return r.t.BarTrack.Render(strings.Repeat("·", from)) +
		bar.Render(strings.Repeat("━", to-from)) +
		r.t.BarTrack.Render(strings.Repeat("·", timelineWidth-to))
}

// dropUninformative removes columns no row has a value for, and related-ID
// columns that are the same on every row, such as the instance of a list of
// its spans. The ID stays.
func dropUninformative(columns []column, rows []*jsjson.Object) []column {
	var kept []column
	for _, c := range columns {
		if c.key == "id" {
			kept = append(kept, c)
			continue
		}
		values := map[string]bool{}
		hasValue := false
		for _, o := range rows {
			v, _ := o.Get(c.key)
			if v != nil && v != "" {
				hasValue = true
			}
			values[jsjson.Stringify(v, "")] = true
		}
		sameRelatedID := strings.HasSuffix(c.key, "_id") && len(rows) > 1 && len(values) == 1
		if hasValue && !sameRelatedID {
			kept = append(kept, c)
		}
	}
	return kept
}

// duration is how long a row's span took, or has been running.
func (r renderer) duration(o *jsjson.Object, pair *[2]string) string {
	start, end, ok := rowSpan(o, pair, r.o.Now)
	if !ok {
		return r.t.Muted.Render("—")
	}
	text := formatDuration(end.Sub(start))
	if v, _ := o.Get(pair[1]); v == nil {
		return r.t.Warning.Render(text + " so far")
	}
	return text
}

// formatDuration writes d in its two largest units, such as 4.2s or 3m 12s.
func formatDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return strconv.FormatFloat(math.Round(d.Seconds()*10)/10, 'f', -1, 64) + "s"
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %dh", int(d.Hours()/24), int(d.Hours())%24)
	}
}
