package js

import (
	"math"
	"testing"
)

func TestTrimMatchesJavaScript(t *testing.T) {
	for in, want := range map[string]string{
		"\uFEFFsk-123\n": "sk-123",
		" \v\f x \t":     "x",
		"\u0085x\u0085":  "\u0085x\u0085",
		" 　x":            "x",
	} {
		if got := Trim(in); got != want {
			t.Errorf("Trim(%q) = %q, want %q", in, got, want)
		}
	}
	if got := TrimStart("\uFEFF 5 "); got != "5 " {
		t.Errorf("TrimStart = %q", got)
	}
}

func TestNumberMatchesJavaScript(t *testing.T) {
	for in, want := range map[string]float64{
		" 5 ": 5, "": 0, "  ": 0, "0x10": 16, "0b11": 3, "0o7": 7, "+2.5": 2.5, ".5": 0.5, "1e3": 1000, "-Infinity": math.Inf(-1),
	} {
		if got := Number(in); got != want {
			t.Errorf("Number(%q) = %v, want %v", in, got, want)
		}
	}
	for _, in := range []string{"1_000", "0x", "abc", "Inf", "NaN", "5s", "0x1p3"} {
		if got := Number(in); !math.IsNaN(got) {
			t.Errorf("Number(%q) = %v, want NaN", in, got)
		}
	}
}
