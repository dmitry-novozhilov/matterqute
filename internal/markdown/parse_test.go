package markdown

import (
	"reflect"
	"testing"
)

func TestToBlocks(t *testing.T) {
	sp := func(text string) []Span { return []Span{{Text: text}} }
	for _, c := range []struct {
		name string
		src  string
		want []Block
	}{
		{"inline", "это **важно** и *очень* ~~не~~ `код` и [ссылка](https://go.dev/a_(b))", []Block{{Kind: Para, Spans: []Span{
			{Text: "это "}, {Text: "важно", Bold: true}, {Text: " и "}, {Text: "очень", Italic: true}, {Text: " "}, {Text: "не", Strike: true},
			{Text: " "}, {Text: "код", Code: true}, {Text: " и "}, {Text: "ссылка", Link: "https://go.dev/a_(b)"}}}}},
		{"nested", "**bold *both* bold**", []Block{{Kind: Para, Spans: []Span{
			{Text: "bold ", Bold: true}, {Text: "both", Bold: true, Italic: true}, {Text: " bold", Bold: true}}}}},
		{"bold link", "[**go** dev](https://go.dev)", []Block{{Kind: Para, Spans: []Span{
			{Text: "go", Bold: true, Link: "https://go.dev"}, {Text: " dev", Link: "https://go.dev"}}}}},
		{"raw stays", `snake_case \*не курсив\* :+1: @alice <https://x.y> ![a](b.png)`, []Block{{Kind: Para,
			Spans: sp(`snake_case \*не курсив\* :+1: @alice <https://x.y> ![a](b.png)`)}}},
		{"lines", "раз\nдва\n\nтри", []Block{{Kind: Para, Spans: sp("раз")}, {Kind: Para, Spans: sp("два")}, {Kind: Para}, {Kind: Para, Spans: sp("три")}}},
		{"lists", "1. a\n   - a1\n2. b\n\nпосле", []Block{{Kind: Ordered, Start: 1, Spans: sp("a")}, {Kind: Bullet, Level: 1, Spans: sp("a1")},
			{Kind: Ordered, Start: 2, Spans: sp("b")}, {Kind: Para, Spans: sp("после")}}},
		{"quote", "> так\n>\n> вот", []Block{{Kind: Quote, Spans: sp("так")}, {Kind: Quote}, {Kind: Quote, Spans: sp("вот")}}},
		{"code", "смотри\n```\nx := **1**\n\n```\nготово", []Block{{Kind: Para, Spans: sp("смотри")}, {Kind: Code, Spans: sp("x := **1**")},
			{Kind: Code, Spans: sp("")}, {Kind: Para, Spans: sp("готово")}}},
		{"heading raw", "## Итоги\n---", []Block{{Kind: Para, Spans: sp("## Итоги")}, {Kind: Para, Spans: sp("---")}}},
	} {
		if got := ToBlocks(c.src); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}

// Сообщение, открытое на правку и сохранённое без изменений, остаётся тем же.
func TestToBlocksRoundTrip(t *testing.T) {
	for _, src := range []string{
		"это **важно** и *очень* ~~не~~ `код`",
		"**bold *both* bold**",
		"see [docs](https://go.dev/doc) и https://go.dev",
		`snake_case \*не курсив\* :+1: @alice ~town-square`,
		"раз\nдва\n\nтри",
		"- раз\n- два\n  - вложенный\n- три\n\nпосле",
		"1. первый\n2. второй",
		"> цитата\n>\n> ещё\n\nответ",
		"смотри\n```\nx := 1\n\n  y()\n```\nготово",
		"```go\nfunc main() {}\n```",
		"## Итоги недели\n| a | b |\n|---|---|\n| 1 | 2 |",
		"![схема](https://x/y.png) и <b>html</b>",
		"``a`b`` и `` `x ``",
		"- пункт\n  продолжение пункта",
	} {
		if got := FromBlocks(ToBlocks(src)); got != src {
			t.Errorf("round trip:\n src %q\n got %q\nblocks %+v", src, got, ToBlocks(src))
		}
	}
}

// Правка без изменений не должна менять текст: номера списка, ленивая
// строка цитаты, код отступом в пункте, заголовок ссылки со скобкой.
func TestToBlocksKeepsSource(t *testing.T) {
	for _, src := range []string{
		"3. third\n4. fourth",
		"2024. год",
		"- a\n\n      code",
		"1. раз\n   - вложенный\n2. два",
	} {
		if got := FromBlocks(ToBlocks(src)); got != src {
			t.Errorf("round trip:\n src %q\n got %q\nblocks %+v", src, got, ToBlocks(src))
		}
	}
	// Ленивая строка остаётся в цитате; заголовок ссылки теряется, но ссылка цела.
	for src, want := range map[string]string{
		"> q\nreply":               "> q\n> reply",
		"> q\n>\nreply":            "> q\n>\n\nreply",
		`[t](url "a)b") хвост`:     "[t](url) хвост",
		`[t](url 'title') и *x*`:   "[t](url) и *x*",
		"[t](https://x/y) и текст": "[t](https://x/y) и текст",
	} {
		if got := FromBlocks(ToBlocks(src)); got != want {
			t.Errorf("ToBlocks(%q) → %q, want %q; blocks %+v", src, got, want, ToBlocks(src))
		}
	}
	if b := ToBlocks("3. third")[0]; b.Kind != Ordered || b.Start != 3 {
		t.Errorf("ordered start: %+v", b)
	}
}
