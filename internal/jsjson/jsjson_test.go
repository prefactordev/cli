package jsjson

import (
	"encoding/json"
	"os"
	"testing"
)

// TestMatchesJavaScript checks Parse and Stringify against
// JSON.stringify(JSON.parse(input), null, 2) from Bun, for random documents
// with array-index and repeated keys, lone surrogates, escapes and edge-case
// numbers, plus inputs JSON.parse rejects. testdata/stringify.json is
// generated with Bun; see the commit that added it.
func TestMatchesJavaScript(t *testing.T) {
	raw, err := os.ReadFile("testdata/stringify.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input string `json:"input"`
		Want  string `json:"want"`
		Error bool   `json:"error"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}

	for i, tc := range cases {
		value, err := Parse([]byte(tc.Input))
		if tc.Error {
			if err == nil {
				t.Errorf("case %d: Parse(%q) succeeded; JSON.parse rejects it", i, tc.Input)
			}
			continue
		}
		if err != nil {
			t.Errorf("case %d: Parse(%q): %v", i, tc.Input, err)
			continue
		}
		if got := Stringify(value, "  "); got != tc.Want {
			t.Errorf("case %d: input %q\n got: %q\nwant: %q", i, tc.Input, got, tc.Want)
		}
	}
}

func TestObjectSetKeepsJavaScriptOrder(t *testing.T) {
	o := NewObject()
	for _, k := range []string{"b", "10", "a", "2", "01", "b"} {
		o.Set(k, Number(1))
	}
	o.Delete("a")
	o.Set("a", Number(2))

	got := Stringify(o, "")
	if want := `{"2":1,"10":1,"b":1,"01":1,"a":2}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
