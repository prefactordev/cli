package human

import (
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"

	"github.com/prefactordev/cli/internal/jsjson"
)

// Options shape the output.
type Options struct {
	// Width is the terminal's width in columns.
	Width int
	// Now is the time relative times are measured from.
	Now time.Time
	// Noun names the listed resources, such as "agents", for an empty list.
	Noun  string
	Theme Theme
}

// Render writes an API response for a person: a table for a list, a card
// for one resource, and the body as text when it is not JSON.
func Render(w io.Writer, raw []byte, o Options) error {
	if o.Width <= 0 {
		o.Width = 80
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	r := renderer{o: o, t: o.Theme}

	var out string
	if len(raw) == 0 {
		out = r.t.Muted.Render("(empty response)")
	} else if value, err := jsjson.Parse(raw); err != nil {
		out = string(raw)
	} else {
		out = r.response(value)
	}
	_, err := io.WriteString(w, strings.TrimRight(out, "\n")+"\n")
	return err
}

type renderer struct {
	o Options
	t Theme
}

// response renders a whole response: its list or details, and any other
// top-level fields, such as a new API token.
func (r renderer) response(value any) string {
	o, ok := value.(*jsjson.Object)
	if !ok {
		return r.value(value, r.o.Width)
	}
	if ok, _ := o.Get("ok"); ok == true && len(o.Keys()) == 1 {
		return r.t.Success.Render("✓") + " Done."
	}

	var parts []string
	shown := map[string]bool{"status": isSuccess(o)}
	if summaries, isList := o.Get("summaries"); isList {
		if rows, isArray := summaries.([]any); isArray {
			parts = append(parts, r.list(rows))
			if pagination, has := o.Get("pagination"); has {
				if footer := r.pagination(pagination); footer != "" {
					parts = append(parts, footer)
				}
			}
			shown["summaries"], shown["pagination"], shown["sorting"] = true, true, true
		}
	}
	if details, has := o.Get("details"); has && len(parts) == 0 {
		switch d := details.(type) {
		case *jsjson.Object:
			parts = append(parts, r.card(d, r.o.Width))
			shown["details"] = true
		case []any:
			parts = append(parts, r.list(d))
			shown["details"] = true
		}
	}

	// Other fields, such as a new API token, follow; empty ones do not.
	rest := jsjson.NewObject()
	for _, key := range o.Keys() {
		if v, _ := o.Get(key); !shown[key] && v != nil {
			rest.Set(key, v)
		}
	}
	if len(rest.Keys()) > 0 {
		parts = append(parts, r.card(rest, r.o.Width))
	}
	if len(parts) == 0 {
		return r.t.Success.Render("✓") + " Done."
	}
	return strings.Join(parts, "\n\n")
}

func isSuccess(o *jsjson.Object) bool {
	status, _ := o.Get("status")
	return status == "success"
}

// value renders any JSON value on its own.
func (r renderer) value(v any, width int) string {
	switch v := v.(type) {
	case *jsjson.Object:
		return r.card(v, width)
	case []any:
		return r.list(v)
	default:
		return r.scalar("", v, false)
	}
}

// labelKeys name a resource, in order of preference.
var labelKeys = []string{"name", "title", "path", "email", "schema_name", "external_identifier"}

// statusKeys hold a status-like value, drawn in its colour.
var statusKeys = []string{"status", "severity", "state", "health", "risk_level", "pathStatus", "installState", "managedBinaryExists"}

// card renders an object as a titled list of fields.
func (r renderer) card(o *jsjson.Object, width int) string {
	var b strings.Builder
	title, inTitle := r.title(o)
	if title != "" {
		b.WriteString(title + "\n")
	}

	type row struct{ key, value string }
	var rows []row
	var sections []string
	for _, key := range o.Keys() {
		if inTitle[key] {
			continue
		}
		v, _ := o.Get(key)
		switch v := v.(type) {
		case *jsjson.Object:
			if flags := r.flags(v); flags != "" {
				rows = append(rows, row{key, flags})
			} else if chart, total := r.barChart(v, width-2); chart != "" {
				heading := key
				if total != "" {
					heading = humanKey(key) + " (" + total + " total)"
				}
				sections = append(sections, r.section(heading, indent(chart, "  ")))
			} else if len(v.Keys()) > 0 {
				sections = append(sections, r.section(key, r.card(v, width-2)))
			} else {
				rows = append(rows, row{key, r.t.Muted.Render("—")})
			}
		case []any:
			if allScalars(v) {
				rows = append(rows, row{key, r.joined(v)})
			} else {
				sections = append(sections, r.section(key, indent(r.list(v), "  ")))
			}
		default:
			value := r.scalar(key, v, false)
			if key == "token" {
				value = r.t.Warning.Render(value) + "  " + r.t.Muted.Render("shown once; store it now")
			}
			rows = append(rows, row{key, value})
		}
	}

	keyWidth := 0
	for _, row := range rows {
		keyWidth = max(keyWidth, lipgloss.Width(humanKey(row.key)))
	}
	for _, row := range rows {
		label := humanKey(row.key)
		value := row.value
		// Text that does not fit continues under the value column.
		if room := width - keyWidth - 4; room > 20 && lipgloss.Width(value) > room && !strings.Contains(value, "\x1b") {
			value = trimLines(lipgloss.NewStyle().Width(room).Render(value))
		}
		value = strings.ReplaceAll(value, "\n", "\n"+strings.Repeat(" ", keyWidth+4))
		b.WriteString("  " + r.t.Muted.Render(label) + strings.Repeat(" ", keyWidth-lipgloss.Width(label)+2) + value + "\n")
	}
	for _, s := range sections {
		b.WriteString("\n" + s)
	}
	return strings.TrimRight(b.String(), "\n")
}

// title is the resource's name and type, if it has them, and the fields it
// shows, which the card leaves out.
func (r renderer) title(o *jsjson.Object) (string, map[string]bool) {
	used := map[string]bool{}
	label := ""
	for _, key := range labelKeys {
		if s, ok := getString(o, key); ok && s != "" {
			label = s
			used[key] = true
			break
		}
	}
	kind, _ := getString(o, "type")
	if kind != "" {
		used["type"] = true
		kind = strings.ReplaceAll(kind, "_", " ")
	}
	switch {
	case label != "" && kind != "":
		return r.t.Title.Render(label) + "  " + r.t.Muted.Render(kind), used
	case label != "":
		return r.t.Title.Render(label), used
	case kind != "":
		return r.t.Title.Render(kind), used
	}
	return "", used
}

// trimLines removes the padding lipgloss adds to the end of wrapped lines.
func trimLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

// section is a heading over a body, which the caller has indented.
func (r renderer) section(key, body string) string {
	heading := key
	if !strings.Contains(key, " ") {
		heading = humanKey(key)
	}
	return r.t.Heading.Render(heading) + "\n" + strings.TrimRight(body, "\n") + "\n"
}

// flags renders an object whose values are all true or false, such as the
// actions a resource allows, on one line.
func (r renderer) flags(o *jsjson.Object) string {
	if len(o.Keys()) == 0 {
		return ""
	}
	var parts []string
	for _, key := range o.Keys() {
		v, _ := o.Get(key)
		b, ok := v.(bool)
		if !ok {
			return ""
		}
		if b {
			parts = append(parts, r.t.Success.Render("✓ ")+humanKey(key))
		} else {
			parts = append(parts, r.t.Muted.Render("✗ "+humanKey(key)))
		}
	}
	return strings.Join(parts, "  ")
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

func allScalars(values []any) bool {
	for _, v := range values {
		switch v.(type) {
		case *jsjson.Object, []any:
			return false
		}
	}
	return true
}

func (r renderer) joined(values []any) string {
	if len(values) == 0 {
		return r.t.Muted.Render("—")
	}
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = r.scalar("", v, true)
	}
	return strings.Join(parts, ", ")
}

// scalar renders one value. compact shortens times for table cells.
func (r renderer) scalar(key string, v any, compact bool) string {
	switch v := v.(type) {
	case nil:
		return r.t.Muted.Render("—")
	case bool:
		if v {
			return r.t.Success.Render("✓ yes")
		}
		return r.t.Muted.Render("✗ no")
	case jsjson.Number:
		return jsjson.FormatNumber(float64(v))
	case string:
		if slices.Contains(statusKeys, key) {
			return r.t.Status(v)
		}
		if at, ok := parseTime(v); ok {
			if compact {
				return relative(at, r.o.Now)
			}
			return at.UTC().Format("2006-01-02 15:04 UTC") + "  " + r.t.Muted.Render(relative(at, r.o.Now))
		}
		return v
	default:
		return fmt.Sprint(v)
	}
}

// barChart draws an object whose values are all numbers, such as instance
// counts, as horizontal bars coloured by what each counts. A total is left
// out of the bars and returned to go in the heading. It is empty for any
// other object.
func (r renderer) barChart(o *jsjson.Object, width int) (string, string) {
	var keys []string
	total := ""
	for _, key := range o.Keys() {
		v, _ := o.Get(key)
		n, ok := v.(jsjson.Number)
		if !ok || float64(n) < 0 {
			return "", ""
		}
		if key == "total" {
			total = jsjson.FormatNumber(float64(n))
			continue
		}
		keys = append(keys, key)
	}
	if len(keys) < 2 {
		return "", ""
	}
	values := make([]float64, len(keys))
	labelWidth, valueWidth, top := 0, 0, 0.0
	for i, key := range keys {
		v, _ := o.Get(key)
		values[i] = float64(v.(jsjson.Number))
		top = max(top, values[i])
		labelWidth = max(labelWidth, lipgloss.Width(humanKey(key)))
		valueWidth = max(valueWidth, len(jsjson.FormatNumber(values[i])))
	}
	barWidth := min(40, width-labelWidth-valueWidth-4)
	if barWidth < 5 {
		return "", ""
	}

	var b strings.Builder
	for i, key := range keys {
		filled := 0
		if top > 0 {
			filled = int(math.Round(values[i] / top * float64(barWidth)))
		}
		if values[i] > 0 && filled == 0 {
			filled = 1
		}
		label := humanKey(key)
		b.WriteString(r.t.Muted.Render(label) + strings.Repeat(" ", labelWidth-lipgloss.Width(label)+2))
		bar := r.t.Bar
		if c := statusColour(key); c != nil {
			bar = lipgloss.NewStyle().Foreground(c)
		}
		b.WriteString(bar.Render(strings.Repeat("█", filled)) + r.t.BarTrack.Render(strings.Repeat("░", barWidth-filled)))
		b.WriteString("  " + jsjson.FormatNumber(values[i]) + "\n")
	}
	return strings.TrimRight(b.String(), "\n"), total
}

// pagination describes where a page sits in the list.
func (r renderer) pagination(v any) string {
	p, ok := v.(*jsjson.Object)
	if !ok {
		return ""
	}
	count, hasCount := getNumber(p, "item_count")
	start, hasStart := getNumber(p, "item_start")
	end, hasEnd := getNumber(p, "item_end")
	if !hasCount {
		return ""
	}
	text := fmt.Sprintf("%s total", plural(int(count), "item", "items"))
	if hasStart && hasEnd && end >= start && count > 0 {
		text = fmt.Sprintf("Showing %s–%s of %s", jsjson.FormatNumber(start), jsjson.FormatNumber(end), jsjson.FormatNumber(count))
	}
	if next, hasNext := getNumber(p, "next_page_offset"); hasNext {
		text += fmt.Sprintf(" · next page: --pagination-offset %s", jsjson.FormatNumber(next))
	}
	return r.t.Muted.Render(text)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func getString(o *jsjson.Object, key string) (string, bool) {
	v, _ := o.Get(key)
	s, ok := v.(string)
	return s, ok
}

func getNumber(o *jsjson.Object, key string) (float64, bool) {
	v, _ := o.Get(key)
	n, ok := v.(jsjson.Number)
	return float64(n), ok
}

// acronyms are written in capitals in field names.
var acronyms = map[string]string{"id": "ID", "ids": "IDs", "url": "URL", "uri": "URI", "api": "API", "llm": "LLM", "sdk": "SDK", "qr": "QR", "os": "OS", "pfid": "PFID"}

// humanKey turns a field name such as external_identifier or binPath into
// "External identifier" or "Bin path".
func humanKey(key string) string {
	var words []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			words = append(words, strings.ToLower(word.String()))
			word.Reset()
		}
	}
	for _, r := range key {
		switch {
		case r == '_' || r == '-' || r == ' ':
			flush()
		case r >= 'A' && r <= 'Z':
			flush()
			word.WriteRune(r)
		default:
			word.WriteRune(r)
		}
	}
	flush()
	for i, w := range words {
		if a, ok := acronyms[w]; ok {
			words[i] = a
		} else if i == 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// parseTime reads an RFC 3339 timestamp.
func parseTime(s string) (time.Time, bool) {
	if len(s) < 20 || s[4] != '-' || s[10] != 'T' {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

// relative says how long ago, or how far ahead, t is.
func relative(t, now time.Time) string {
	d := now.Sub(t)
	suffix := " ago"
	if d < 0 {
		d, suffix = -d, ""
	}
	var amount string
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		amount = fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		amount = fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		amount = fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		amount = fmt.Sprintf("%dmo", int(d.Hours()/24/30))
	default:
		amount = fmt.Sprintf("%dy", int(d.Hours()/24/365))
	}
	if suffix == "" {
		return "in " + amount
	}
	return amount + suffix
}

// newTable is a table with a rule under the header and no other borders.
func (r renderer) newTable() *table.Table {
	return table.New().
		Border(lipgloss.NormalBorder()).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderColumn(false).BorderRow(false).BorderHeader(true).
		BorderStyle(r.t.TableEdge)
}
