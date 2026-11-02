package markdown

import (
	"strings"
	"testing"
)

func TestClip(t *testing.T) {
	lines := func(n int) string {
		var b strings.Builder
		for i := range n {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString("- строка")
		}
		return b.String()
	}
	for _, c := range []struct {
		name, in string
		long     bool
		want     string
	}{
		{"short", "привет\nмир", false, "привет\nмир"},
		{"many lines", lines(30), true, lines(12)},
		{"code fence closed", "текст\n```go\n" + lines(30), true, "текст\n```go\n" + lines(10) + "\n```"},
		{"closed fence stays", "```\nкод\n```\n" + lines(30), true, "```\nкод\n```\n" + lines(9)},
		{"one long line by words", strings.Repeat("слово ", 600), true, strings.TrimRight(strings.Repeat("слово ", 200), " ") + "…"},
	} {
		if got := Long(c.in, 20, 2000); got != c.long {
			t.Errorf("%s: Long = %v, want %v", c.name, got, c.long)
		}
		if got := Clip(c.in, 12, 1200); got != c.want {
			t.Errorf("%s: Clip =\n%q\nwant\n%q", c.name, got, c.want)
		}
	}
}
