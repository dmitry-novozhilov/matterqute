package markdown

import "testing"

func TestInline(t *testing.T) {
	for _, c := range []struct {
		name  string
		spans []Span
		want  string
	}{
		{"plain passes through", []Span{{Text: "a *b* `c` ~d"}}, "a *b* `c` ~d"},
		{"bold", []Span{{Text: "сказал "}, {Text: "важное", Bold: true}, {Text: " слово"}}, "сказал **важное** слово"},
		{"spaces out of markers", []Span{{Text: "a"}, {Text: " жирный ", Bold: true}, {Text: "b"}}, "a **жирный** b"},
		{"nested", []Span{{Text: "bold ", Bold: true}, {Text: "both", Bold: true, Italic: true}, {Text: " bold", Bold: true}}, "**bold *both* bold**"},
		{"overlap reopens", []Span{{Text: "b", Bold: true}, {Text: "bi", Bold: true, Italic: true}, {Text: "i", Italic: true}}, "**b*bi***\u002ai*"},
		{"strike", []Span{{Text: "old", Strike: true}, {Text: " new"}}, "~~old~~ new"},
		{"code", []Span{{Text: "run "}, {Text: "make test", Code: true}}, "run `make test`"},
		{"code with backticks", []Span{{Text: "a`b", Code: true}}, "``a`b``"},
		{"code edge backtick", []Span{{Text: "`x", Code: true}}, "`` `x ``"},
		{"bold code", []Span{{Text: "x", Code: true, Bold: true}}, "**`x`**"},
		{"link", []Span{{Text: "see "}, {Text: "docs", Link: "https://example.com/a b"}}, "see [docs](https://example.com/a%20b)"},
		{"autolink", []Span{{Text: "https://go.dev", Link: "https://go.dev"}}, "https://go.dev"},
		{"bold link text", []Span{{Text: "go", Bold: true, Link: "https://go.dev"}, {Text: " dev", Link: "https://go.dev"}}, "[**go** dev](https://go.dev)"},
		{"link brackets", []Span{{Text: "[x]", Link: "https://x"}}, `[\[x\]](https://x)`},
		{"whitespace-only span", []Span{{Text: "a", Bold: true}, {Text: " ", Italic: true}, {Text: "b", Bold: true}}, "**a b**"},
	} {
		if got := Inline(c.spans); got != c.want {
			t.Errorf("%s: Inline = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFromBlocks(t *testing.T) {
	p := func(kind BlockKind, level int, text string) Block {
		return Block{Kind: kind, Level: level, Spans: []Span{{Text: text}}}
	}
	for _, c := range []struct {
		name   string
		blocks []Block
		want   string
	}{
		{"lines", []Block{p(Para, 0, "раз"), p(Para, 0, "два"), p(Para, 0, ""), p(Para, 0, "три")}, "раз\nдва\n\nтри"},
		{"trim", []Block{p(Para, 0, ""), p(Para, 0, "a"), p(Para, 0, "")}, "a"},
		{"bullets then para", []Block{p(Bullet, 0, "a"), p(Bullet, 0, "b"), p(Para, 0, "после")}, "- a\n- b\n\nпосле"},
		{"ordered", []Block{p(Ordered, 0, "a"), p(Ordered, 0, "b"), p(Ordered, 0, "c")}, "1. a\n2. b\n3. c"},
		{"nested", []Block{p(Ordered, 0, "a"), p(Bullet, 1, "a1"), p(Bullet, 1, "a2"), p(Ordered, 0, "b"), p(Bullet, 1, "b1"), p(Bullet, 2, "b1x")},
			"1. a\n   - a1\n   - a2\n2. b\n   - b1\n     - b1x"},
		{"jump levels", []Block{p(Bullet, 0, "a"), p(Bullet, 3, "deep")}, "- a\n  - deep"},
		{"kind change", []Block{p(Bullet, 0, "a"), p(Ordered, 0, "b")}, "- a\n1. b"},
		{"empty item", []Block{p(Bullet, 0, "a"), p(Bullet, 0, "")}, "- a\n-"},
		{"quote", []Block{p(Para, 0, "он сказал:"), p(Quote, 0, "так"), p(Quote, 0, ""), p(Quote, 0, "вот"), p(Para, 0, "ок")}, "он сказал:\n> так\n>\n> вот\n\nок"},
		{"code", []Block{p(Para, 0, "смотри"), p(Code, 0, "x := 1"), p(Code, 0, ""), p(Code, 0, "  y()"), p(Para, 0, "готово")},
			"смотри\n```\nx := 1\n\n  y()\n```\nготово"},
		{"code with fence inside", []Block{p(Code, 0, "```go"), p(Code, 0, "```")}, "````\n```go\n```\n````"},
		{"code is raw", []Block{{Kind: Code, Spans: []Span{{Text: "a*b", Bold: true}}}}, "```\na*b\n```"},
		{"list after quote", []Block{p(Quote, 0, "q"), p(Bullet, 0, "a")}, "> q\n\n- a"},
		{"code after list", []Block{p(Bullet, 0, "a"), p(Code, 0, "x")}, "- a\n\n```\nx\n```"},
		{"soft break in item", []Block{{Kind: Bullet, Spans: []Span{{Text: "a\u2028b"}}}}, "- a\n  b"},
	} {
		if got := FromBlocks(c.blocks); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}
