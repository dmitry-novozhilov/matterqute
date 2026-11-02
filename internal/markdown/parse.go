package markdown

import (
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// ToBlocks — сообщение (markdown Mattermost) абзацами редактора, обратное к
// FromBlocks: правка своего сообщения идёт в том же поле с оформлением.
// Строки разбираются сами (списки, цитаты, блоки кода), строка внутри — через
// goldmark: жирный, курсив, зачёркнутый, код, ссылки. Чего редактор не умеет
// (заголовки, таблицы, картинки, HTML, код с языком), остаётся исходным
// текстом: FromBlocks вернёт его как был.
func ToBlocks(src string) []Block {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var out []Block
	type item struct{ marker, content int } // отступы маркера и текста открытых пунктов
	var items []item
	fence := ""       // открытый блок кода
	rawFence := false // блок кода с языком — текстом как есть
	skipped := false  // пустая строка после списка или цитаты: её ставит FromBlocks
	last := func() *Block {
		if len(out) == 0 {
			return nil
		}
		return &out[len(out)-1]
	}
	for _, l := range lines {
		if fence != "" {
			if t := strings.TrimSpace(l); strings.HasPrefix(t, fence) && strings.Trim(t, fence[:1]) == "" {
				fence = ""
				if rawFence {
					out = append(out, plainBlock(l))
				}
				continue
			}
			if rawFence {
				out = append(out, plainBlock(l))
			} else {
				out = append(out, Block{Kind: Code, Spans: []Span{{Text: l}}})
			}
			continue
		}
		if strings.TrimSpace(l) == "" {
			if b := last(); b != nil && (b.Kind == Bullet || b.Kind == Ordered || b.Kind == Quote) && !skipped {
				skipped = true
				continue
			}
			out = append(out, Block{Kind: Para})
			items = nil
			continue
		}
		skipped = false
		if m := fenceRe.FindStringSubmatch(l); m != nil {
			fence, items = m[1], nil
			rawFence = strings.TrimSpace(m[2]) != ""
			if rawFence {
				out = append(out, plainBlock(l))
			}
			continue
		}
		if m := listItemRe.FindStringSubmatch(l); m != nil {
			indent, marker := len(m[1]), m[2]
			for len(items) > 0 && indent < items[len(items)-1].content {
				items = items[:len(items)-1]
			}
			kind := Bullet
			if strings.ContainsAny(marker, ".)") {
				kind = Ordered
			}
			out = append(out, Block{Kind: kind, Level: len(items), Spans: inlineSpans(m[3])})
			items = append(items, item{indent, indent + len(marker) + 1})
			continue
		}
		if m := quoteRe.FindStringSubmatch(l); m != nil {
			out = append(out, Block{Kind: Quote, Spans: inlineSpans(m[1])})
			items = nil
			continue
		}
		if b := last(); b != nil && (b.Kind == Bullet || b.Kind == Ordered) && len(items) > 0 &&
			len(l)-len(strings.TrimLeft(l, " ")) >= items[len(items)-1].content {
			// Продолжение пункта с новой строки.
			b.Spans = appendSpans(b.Spans, Span{Text: " "})
			b.Spans = appendSpans(b.Spans, inlineSpans(strings.TrimLeft(l, " "))...)
			continue
		}
		items = nil
		out = append(out, Block{Kind: Para, Spans: inlineSpans(l)})
	}
	for len(out) > 0 && out[len(out)-1].Kind == Para && len(out[len(out)-1].Spans) == 0 {
		out = out[:len(out)-1]
	}
	return out
}

var (
	fenceRe    = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})(.*)$")
	listItemRe = regexp.MustCompile(`^( *)([-*+]|\d{1,9}[.)])(?: (.*))?$`)
	quoteRe    = regexp.MustCompile(`^ {0,3}> ?(.*)$`)
)

func plainBlock(s string) Block {
	return Block{Kind: Para, Spans: []Span{{Text: s}}}
}

func appendSpans(spans []Span, more ...Span) []Span {
	for _, sp := range more {
		if sp.Text == "" {
			continue
		}
		if n := len(spans); n > 0 && sameSpanStyle(spans[n-1], sp) {
			spans[n-1].Text += sp.Text
		} else {
			spans = append(spans, sp)
		}
	}
	return spans
}

func sameSpanStyle(a, b Span) bool {
	a.Text, b.Text = "", ""
	return a == b
}

var editorParser = goldmark.New(goldmark.WithExtensions(extension.Strikethrough)).Parser()

// inlineSpans — строка с оформлением. Всё, что не жирный, курсив,
// зачёркнутый, код и ссылка, остаётся исходным текстом — с экранированием,
// автоссылками, эмодзи, упоминаниями.
func inlineSpans(s string) []Span {
	src := []byte(s)
	doc := editorParser.Parse(text.NewReader(src))
	p := doc.FirstChild()
	if p == nil || p.Kind() != ast.KindParagraph || p.NextSibling() != nil {
		return appendSpans(nil, Span{Text: s}) // заголовок, разделитель и т. п. — как есть
	}
	var out []Span
	var emit func(parent ast.Node, st Span, from, to int)
	emit = func(parent ast.Node, st Span, from, to int) {
		pos := from
		for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
			start, end, ok := rawRange(c, src)
			if !ok || start < pos || end > to {
				continue // не наше — останется в исходном тексте вокруг
			}
			inner := st
			switch n := c.(type) {
			case *ast.Emphasis:
				if n.Level >= 2 {
					inner.Bold = true
				} else {
					inner.Italic = true
				}
			case *east.Strikethrough:
				inner.Strike = true
			case *ast.CodeSpan:
				inner.Code = true
			case *ast.Link:
				inner.Link = string(n.Destination)
			default:
				continue
			}
			out = appendSpans(out, plainOf(src[pos:start], st))
			inFrom, inTo := innerRange(c, src, start, end)
			if inner.Code {
				code := string(src[inFrom:inTo])
				if len(code) > 1 && code[0] == ' ' && code[len(code)-1] == ' ' && strings.Trim(code, " ") != "" {
					code = code[1 : len(code)-1]
				}
				inner.Text = code
				out = appendSpans(out, inner)
			} else {
				emit(c, inner, inFrom, inTo)
			}
			pos = end
		}
		out = appendSpans(out, plainOf(src[pos:to], st))
	}
	emit(p, Span{}, 0, len(src))
	return out
}

func plainOf(b []byte, st Span) Span {
	st.Text = string(b)
	return st
}

// rawRange — где узел в исходной строке, с разделителями. Узел, место
// которого не вычислить (картинка, автоссылка, HTML), — ok=false.
func rawRange(n ast.Node, src []byte) (start, end int, ok bool) {
	if t, isText := n.(*ast.Text); isText {
		return t.Segment.Start, t.Segment.Stop, true
	}
	first, last := n.FirstChild(), n.LastChild()
	if first == nil {
		return 0, 0, false
	}
	start, _, ok1 := rawRange(first, src)
	_, end, ok2 := rawRange(last, src)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	run := func(at, step int, c byte, limit int) int { // символов c подряд от at в сторону step
		k := 0
		for i := at; i >= 0 && i < len(src) && src[i] == c && k < limit; i += step {
			k++
		}
		return k
	}
	switch n := n.(type) {
	case *ast.Emphasis:
		if start < n.Level {
			return 0, 0, false
		}
		c := src[start-1]
		if c != '*' && c != '_' || run(start-1, -1, c, n.Level) != n.Level || run(end, 1, c, n.Level) != n.Level {
			return 0, 0, false
		}
		return start - n.Level, end + n.Level, true
	case *east.Strikethrough:
		k := run(start-1, -1, '~', 2)
		if k == 0 || run(end, 1, '~', k) != k {
			return 0, 0, false
		}
		return start - k, end + k, true
	case *ast.CodeSpan:
		s, e := start, end
		if s > 0 && src[s-1] == ' ' && e < len(src) && src[e] == ' ' {
			s, e = s-1, e+1
		}
		k := run(s-1, -1, '`', len(src))
		if k == 0 || run(e, 1, '`', k) != k {
			return 0, 0, false
		}
		return s - k, e + k, true
	case *ast.Link:
		if start < 1 || src[start-1] != '[' || end+1 >= len(src) || src[end] != ']' || src[end+1] != '(' {
			return 0, 0, false
		}
		depth := 0
		for i := end + 2; i < len(src); i++ {
			switch src[i] {
			case '\\':
				i++
			case '(':
				depth++
			case ')':
				if depth == 0 {
					return start - 1, i + 1, true
				}
				depth--
			}
		}
	}
	return 0, 0, false
}

// innerRange — содержимое узла без разделителей: текст ссылки, код без кавычек.
func innerRange(n ast.Node, src []byte, start, end int) (int, int) {
	switch n := n.(type) {
	case *ast.Emphasis:
		return start + n.Level, end - n.Level
	case *east.Strikethrough:
		k := 0
		for k < 2 && src[start+k] == '~' {
			k++
		}
		return start + k, end - k
	case *ast.CodeSpan:
		k := 0
		for src[start+k] == '`' {
			k++
		}
		return start + k, end - k
	case *ast.Link:
		s, _, _ := rawRange(n.FirstChild(), src)
		_, e, _ := rawRange(n.LastChild(), src)
		return s, e
	}
	return start, end
}
