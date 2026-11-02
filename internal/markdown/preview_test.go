package markdown

import (
	"strings"
	"testing"
)

func TestPreviewLine(t *testing.T) {
	for _, c := range []struct{ src, want, not string }{
		{"Релиз **v2** готов\nвторая строка", "Релиз <strong>v2</strong> готов", "вторая"},
		{"- пункт *курсив*\n- ещё", "пункт <em>курсив</em>", "<li>"},
		{"> - цитата со списком", "цитата со списком", "<table"},
		{"## Заголовок", "Заголовок", "font-size:20px"},
		{"```go\nx := `a`\n```", "x := `a`", "<table"},
		{"---\n\n1. первый", "первый", "<hr"},
		{"смотри `код` :+1:", "👍", "<p"},
	} {
		got := PreviewLine(c.src, Options{})
		if !strings.Contains(got, c.want) || strings.Contains(got, c.not) {
			t.Errorf("PreviewLine(%q) = %q; want %q, without %q", c.src, got, c.want, c.not)
		}
	}
	if got := PreviewLine("\n  \n", Options{}); got != "" {
		t.Errorf("empty message preview %q", got)
	}
}
