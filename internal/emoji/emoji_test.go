package emoji

import "testing"

func TestDisplay(t *testing.T) {
	for name, want := range map[string]string{
		"+1":           "👍",
		"rainbow_flag": "🏳️‍🌈",
		"flag-ru":      "🇷🇺",
		"partyparrot":  ":partyparrot:", // кастомный
	} {
		if got := Display(name); got != want {
			t.Errorf("Display(%q) = %q, want %q", name, got, want)
		}
	}
	for _, name := range Quick {
		if _, ok := Char(name); !ok {
			t.Errorf("quick emoji %q is not a system emoji", name)
		}
	}
}
