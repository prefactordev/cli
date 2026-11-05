package human

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

var now = time.Date(2026, 11, 5, 2, 0, 0, 0, time.UTC)

// render returns the output without colour, at width.
func render(t *testing.T, body string, width int) string {
	t.Helper()
	var b bytes.Buffer
	if err := Render(&b, []byte(body), Options{Width: width, Now: now, Noun: "agents", Theme: DefaultTheme()}); err != nil {
		t.Fatal(err)
	}
	return ansi.Strip(b.String())
}

func lines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

const agents = `{"status":"success","summaries":[
 {"type":"agent","id":"a1","name":"Support bot","status":"active","role":"standard","team_id":null,"account_id":"acc","inserted_at":"2026-11-05T01:30:00Z"},
 {"type":"agent","id":"a2","name":"Billing judge","status":"retired","role":"judge","team_id":null,"account_id":"acc","inserted_at":"2026-10-01T00:00:00Z"}],
 "pagination":{"item_count":49,"item_start":1,"item_end":2,"next_page_offset":2},"sorting":null}`

func TestListsAreTables(t *testing.T) {
	out := lines(render(t, agents, 120))
	want := []string{
		" NAME           STATUS   ROLE      INSERTED AT  ID",
		"───────────────────────────────────────────────────",
		" Support bot    active   standard  30m ago      a1",
		" Billing judge  retired  judge     1mo ago      a2",
		"",
		"Showing 1–2 of 49 · next page: --pagination-offset 2",
	}
	if strings.Join(out, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(out, "\n"), strings.Join(want, "\n"))
	}
}

func TestNarrowTablesDropTheLeastUsefulColumns(t *testing.T) {
	// The time goes first, then classifiers such as the role; the name,
	// status and ID stay.
	for width, want := range map[int]string{40: "NAME STATUS ROLE ID", 30: "NAME STATUS ID"} {
		if header := strings.Join(strings.Fields(lines(render(t, agents, width))[0]), " "); header != want {
			t.Errorf("width %d: header %q, want %q", width, header, want)
		}
	}
}

func TestEmptyListsSayWhatIsMissing(t *testing.T) {
	if out := render(t, `{"summaries":[]}`, 80); out != "No agents found.\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestSpansGetADurationAndATimeline(t *testing.T) {
	out := render(t, `{"summaries":[
		{"id":"s1","schema_name":"llm","status":"complete","started_at":"2026-11-05T01:00:00Z","finished_at":"2026-11-05T01:00:30Z"},
		{"id":"s2","schema_name":"tool","status":"active","started_at":"2026-11-05T01:00:30Z","finished_at":null}]}`, 120)
	rows := lines(out)
	if !strings.Contains(rows[0], "DURATION") || !strings.Contains(rows[0], "TIMELINE") {
		t.Fatalf("header = %q", rows[0])
	}
	if !strings.Contains(rows[2], "30s") || !strings.Contains(rows[3], "59m 30s so far") {
		t.Fatalf("durations:\n%s", out)
	}
	// The first span takes the start of the timeline, the running one the rest.
	if !strings.Contains(rows[2], "━·") || !strings.HasSuffix(strings.TrimSpace(rows[3]), "━") {
		t.Fatalf("timeline:\n%s", out)
	}
}

func TestRepeatedRelatedIDsAreLeftOut(t *testing.T) {
	out := render(t, `{"summaries":[{"id":"s1","agent_instance_id":"i1","status":"complete"},{"id":"s2","agent_instance_id":"i1","status":"failed"}]}`, 120)
	if strings.Contains(out, "AGENT INSTANCE ID") {
		t.Fatalf("out:\n%s", out)
	}
}

func TestDetailsAreCards(t *testing.T) {
	out := render(t, `{"status":"success","details":{"type":"agent","id":"a1","name":"Support bot","status":"active","description":null,
		"inserted_at":"2026-11-03T02:00:00Z","available_actions":{"retire":true,"reinstate":false},
		"instance_counts":{"total":6,"complete":4,"failed":2}}}`, 100)
	want := []string{
		"Support bot  agent",
		"  ID                 a1",
		"  Status             active",
		"  Description        —",
		"  Inserted at        2026-11-03 02:00 UTC  2d ago",
		"  Available actions  ✓ Retire  ✗ Reinstate",
		"",
		"Instance counts (6 total)",
		"  Complete  ████████████████████████████████████████  4",
		"  Failed    ████████████████████░░░░░░░░░░░░░░░░░░░░  2",
	}
	if strings.Join(lines(out), "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", out, strings.Join(want, "\n"))
	}
}

func TestOtherResponses(t *testing.T) {
	for body, want := range map[string]string{
		`{"ok":true}`: "✓ Done.\n",
		`not json`:    "not json\n",
		``:            "(empty response)\n",
		`{"details":{"id":"t1"},"token":"pf_abc"}`: "  ID  t1\n\n  Token  pf_abc  shown once; store it now\n",
	} {
		if out := render(t, body, 80); out != want {
			t.Errorf("%s: got %q, want %q", body, out, want)
		}
	}
}

func TestHumanKeys(t *testing.T) {
	for in, want := range map[string]string{"external_identifier": "External identifier", "agent_id": "Agent ID", "binPath": "Bin path", "api_url": "API URL"} {
		if got := humanKey(in); got != want {
			t.Errorf("humanKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBannerFitsItsWidth(t *testing.T) {
	for _, line := range lines(ansi.Strip(Banner())) {
		if w := len([]rune(line)); w != BannerWidth() {
			t.Fatalf("line width %d, want %d: %q", w, BannerWidth(), line)
		}
	}
}
