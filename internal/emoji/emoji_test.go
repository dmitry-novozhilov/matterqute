package emoji

import "testing"

func TestDisplay(t *testing.T) {
	for name, want := range map[string]string{
		"+1":           "👍",
		"rainbow_flag": "\U0001F3F3\u200D\U0001F308", // без fe0f внутри: иначе Qt рисует по частям
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
