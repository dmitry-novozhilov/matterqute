package markdown

import (
	"regexp"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"

	"matterqute/internal/emoji"
)

var (
	emojiCodeRe = regexp.MustCompile(`:([a-zA-Z0-9_+-]{1,64}):`)
	blankLines  = regexp.MustCompile(`\n{2,}`)
)

// PlainText — текст сообщения без разметки (для уведомлений): ссылки — своим
// текстом, код — как есть, :эмодзи: — символом, блоки — с новой строки.
func PlainText(src string) string {
	source := []byte(src)
	doc := plainRenderer.md.Parser().Parse(text.NewReader(source))
	var b strings.Builder
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			if n.Type() == ast.TypeBlock && n.Kind() != ast.KindDocument && n.NextSibling() != nil {
				b.WriteByte('\n')
			}
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Text:
			b.Write(n.Segment.Value(source))
			switch {
			case n.HardLineBreak():
				b.WriteByte('\n')
			case n.SoftLineBreak():
				b.WriteByte('\n') // у Mattermost одиночный перевод строки — перенос
			}
		case *ast.String:
			b.Write(n.Value)
		case *ast.AutoLink:
			b.Write(n.URL(source))
		case *ast.FencedCodeBlock, *ast.CodeBlock, *ast.HTMLBlock:
			lines := n.Lines()
			for i := range lines.Len() {
				seg := lines.At(i)
				b.Write(seg.Value(source))
			}
			return ast.WalkSkipChildren, nil
		case *ast.RawHTML:
			for i := range n.Segments.Len() {
				seg := n.Segments.At(i)
				b.Write(seg.Value(source))
			}
		}
		return ast.WalkContinue, nil
	})
	out := emojiCodeRe.ReplaceAllStringFunc(b.String(), func(code string) string {
		if s, ok := emoji.Char(code[1 : len(code)-1]); ok {
			return s
		}
		return code
	})
	return strings.TrimSpace(blankLines.ReplaceAllString(out, "\n"))
}
