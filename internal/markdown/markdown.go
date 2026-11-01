// Package markdown превращает текст сообщения Mattermost в HTML из
// подмножества, которое понимает Qt rich text (QLabel).
//
// Разметка — CommonMark + GFM (таблицы, зачёркивание, автоссылки, чек-листы)
// с особенностями Mattermost: одиночный перевод строки — это <br>, сырой HTML
// показывается текстом, @упоминания, ~каналы и :эмодзи:.
package markdown

import (
	"bytes"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	ghtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"matterqute/internal/emoji"
	"matterqute/internal/theme"
)

// Ссылки на сущности Mattermost внутри текста; их обрабатывает UI, а не браузер.
const (
	UserLinkScheme    = "mm-user:"
	ChannelLinkScheme = "mm-channel:"
)

// Options — контекст, без которого разметку не отрисовать.
type Options struct {
	Me string // username: его упоминания подсвечиваются
	// ChannelExists — есть ли канал с таким именем; ~имя становится ссылкой только тогда.
	ChannelExists func(name string) bool
	// CustomEmoji — путь к картинке кастомного эмодзи на диске, "" — нет (или ещё
	// не загружена: вызывающий сам решает, догружать ли и перерисовывать).
	CustomEmoji func(name string) string
	// Palette — цвета темы для упоминаний, кода, цитат, таблиц; пусто — светлая.
	Palette theme.Palette
}

// Renderer не потокобезопасен: держите по одному на поток (у нас — главный).
type Renderer struct {
	md  goldmark.Markdown
	pal theme.Palette
}

func New(o Options) *Renderer {
	if o.Palette.Acc == "" {
		o.Palette = theme.Light
	}
	return &Renderer{pal: o.Palette, md: goldmark.New(
		goldmark.WithExtensions(extension.Strikethrough, extension.Table, extension.Linkify, extension.TaskList),
		goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(mergeText{}, 10000))),
		goldmark.WithRendererOptions(
			ghtml.WithHardWraps(),
			renderer.WithNodeRenderers(util.Prioritized(&qtRenderer{o: o, Config: ghtml.NewConfig()}, 100)),
		),
	)}
}

var plainRenderer = New(Options{})

// Render — разметка без ~каналов и кастомных эмодзи, светлая тема.
func Render(text string) string { return plainRenderer.Render(text) }

// RenderInline — короткий текст в одну строку (заголовок канала): без
// обёртки-абзаца, переносы строк — пробелами.
func RenderInline(text string, pal theme.Palette) string {
	out := New(Options{Palette: pal}).Render(strings.Join(strings.Fields(text), " "))
	out = strings.TrimPrefix(out, `<p style="margin:0">`)
	return strings.TrimSuffix(out, "</p>")
}

func (r *Renderer) Render(text string) string {
	var buf bytes.Buffer
	if err := r.md.Convert([]byte(text), &buf); err != nil {
		// Конвертер не падает на любом входе, но если вдруг — покажем текст как есть.
		return strings.ReplaceAll(html.EscapeString(text), "\n", "<br>")
	}
	return strings.TrimSpace(buf.String())
}

// mergeText склеивает соседние текстовые узлы, идущие в исходнике подряд.
// Парсер режет текст по символам-разделителям (_ ~ *) даже там, где разметки
// не вышло, и «:white_check_mark:» или «~town-square» оказались бы в разных узлах.
type mergeText struct{}

func (mergeText) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			t, ok := c.(*ast.Text)
			if !ok {
				continue
			}
			for next := t.NextSibling(); next != nil && !t.SoftLineBreak() && !t.HardLineBreak() && t.Merge(next, source); next = t.NextSibling() {
				n.RemoveChild(n, next)
			}
		}
		return ast.WalkContinue, nil
	})
}

// qtRenderer подменяет узлы, которые Qt рисует плохо или которые у
// Mattermost значат другое; остальное рисует стандартный HTML-рендер goldmark.
type qtRenderer struct {
	ghtml.Config
	o Options
}

func (r *qtRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindText, r.text)
	reg.Register(ast.KindParagraph, r.paragraph)
	reg.Register(ast.KindCodeSpan, r.codeSpan)
	reg.Register(ast.KindFencedCodeBlock, r.codeBlock)
	reg.Register(ast.KindCodeBlock, r.codeBlock)
	reg.Register(ast.KindHTMLBlock, r.htmlBlock)
	reg.Register(ast.KindRawHTML, r.rawHTML)
	reg.Register(ast.KindImage, r.image)
	reg.Register(ast.KindBlockquote, r.blockquote)
	reg.Register(ast.KindHeading, r.heading)
	reg.Register(east.KindTable, r.table)
	reg.Register(east.KindTableCell, r.tableCell)
	reg.Register(east.KindTaskCheckBox, r.taskCheckBox)
}

// paragraph: у <p> в Qt большие поля по умолчанию; между абзацами — небольшой отступ.
func (r *qtRenderer) paragraph(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		w.WriteString("</p>\n")
		return ast.WalkContinue, nil
	}
	if n.PreviousSibling() != nil {
		w.WriteString(`<p style="margin-top:6px;margin-bottom:0">`)
	} else {
		w.WriteString(`<p style="margin:0">`)
	}
	return ast.WalkContinue, nil
}

func (r *qtRenderer) text(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.Text)
	value := n.Segment.Value(source)
	switch {
	case n.IsRaw():
		r.Writer.RawWrite(w, value)
	case insideLink(n):
		r.Writer.Write(w, value)
	default:
		r.writeEntities(w, value)
	}
	if n.HardLineBreak() || n.SoftLineBreak() {
		w.WriteString("<br>\n") // Mattermost: каждый перевод строки — перенос
	}
	return ast.WalkContinue, nil
}

func insideLink(n ast.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if k := p.Kind(); k == ast.KindLink || k == ast.KindAutoLink || k == ast.KindImage {
			return true
		}
	}
	return false
}

var entityRe = regexp.MustCompile(`@[a-zA-Z0-9][a-zA-Z0-9._-]*|~[a-z0-9][a-z0-9_-]*|:[a-zA-Z0-9_+-]+:`)

// writeEntities пишет текст, превращая @упоминания, ~каналы и :эмодзи: в разметку.
func (r *qtRenderer) writeEntities(w util.BufWriter, value []byte) {
	last := 0
	for _, m := range entityRe.FindAllIndex(value, -1) {
		start, end := m[0], m[1]
		var out string
		switch value[start] {
		case '@', '~':
			if start > 0 && isWordByte(value[start-1]) {
				continue // bob@example.com, a~b
			}
			if value[start] == '@' {
				end = start + len(strings.TrimRight(string(value[start:end]), "._-"))
				out = r.mention(string(value[start+1 : end]))
			} else {
				out = r.channel(string(value[start+1 : end]))
			}
		case ':':
			// Как у Mattermost: эмодзи отделён пробелами или пунктуацией,
			// иначе «12:30:45» превратилось бы в картинку.
			if start > 0 && !isSpaceOrOpen(value[start-1]) || end < len(value) && !isSpaceOrClose(value[end]) {
				continue
			}
			out = r.emoji(string(value[start+1 : end-1]))
		}
		if out == "" {
			continue
		}
		r.Writer.Write(w, value[last:start])
		w.WriteString(out)
		last = end
	}
	r.Writer.Write(w, value[last:])
}

func (r *qtRenderer) mention(name string) string {
	p := r.o.Palette
	lower := strings.ToLower(name)
	style := fmt.Sprintf("color:%s;font-weight:600;text-decoration:none", p.Acc)
	if lower == "channel" || lower == "all" || lower == "here" || (r.o.Me != "" && lower == strings.ToLower(r.o.Me)) {
		style = fmt.Sprintf("background-color:%s;color:%s;font-weight:600;text-decoration:none", p.MenBg, p.MenTx)
	}
	return `<a href="` + UserLinkScheme + url.PathEscape(lower) + `" style="` + style + `">@` + html.EscapeString(name) + `</a>`
}

func (r *qtRenderer) channel(name string) string {
	if r.o.ChannelExists == nil || !r.o.ChannelExists(name) {
		return ""
	}
	return `<a href="` + ChannelLinkScheme + url.PathEscape(name) + `" style="color:` + r.o.Palette.Acc + `">~` + html.EscapeString(name) + `</a>`
}

func (r *qtRenderer) emoji(name string) string {
	if s, ok := emoji.Char(name); ok {
		return s
	}
	if r.o.CustomEmoji == nil {
		return ""
	}
	path := r.o.CustomEmoji(name)
	if path == "" {
		return ""
	}
	return `<img src="` + html.EscapeString((&url.URL{Scheme: "file", Path: path}).String()) +
		`" width="20" height="20" title=":` + html.EscapeString(name) + `:">`
}

func isWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
}

func isSpaceOrOpen(b byte) bool  { return strings.IndexByte(" \t\n([{\"'", b) >= 0 }
func isSpaceOrClose(b byte) bool { return strings.IndexByte(" \t\n.,;:!?)]}\"'", b) >= 0 }

func (r *qtRenderer) codeSpan(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	w.WriteString(`<span style="font-family:'PT Mono';font-size:13px;background-color:` + r.o.Palette.Surf2 + `">`)
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		v := c.(*ast.Text).Segment.Value(source)
		r.Writer.RawWrite(w, bytes.TrimSuffix(v, []byte("\n")))
		if bytes.HasSuffix(v, []byte("\n")) {
			w.WriteByte(' ')
		}
	}
	w.WriteString("</span>")
	return ast.WalkSkipChildren, nil
}

// codeBlock: <pre> в Qt не переносит строки и распирает ленту вширь, поэтому
// pre-wrap в ячейке таблицы — она же даёт фон.
func (r *qtRenderer) codeBlock(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	w.WriteString(`<table width="100%" cellspacing="0" cellpadding="8" bgcolor="` + r.o.Palette.Surf2 + `" style="margin-top:4px;margin-bottom:4px"><tr><td>` +
		`<p style="margin:0;white-space:pre-wrap;font-family:'PT Mono';font-size:13px">`)
	lines := n.Lines()
	for i := range lines.Len() {
		seg := lines.At(i)
		v := seg.Value(source)
		if i == lines.Len()-1 {
			v = bytes.TrimSuffix(v, []byte("\n"))
		}
		r.Writer.RawWrite(w, v)
	}
	w.WriteString("</p></td></tr></table>\n")
	return ast.WalkSkipChildren, nil
}

// htmlBlock и rawHTML: Mattermost HTML не исполняет, а показывает как текст.
func (r *qtRenderer) htmlBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.HTMLBlock)
	var b bytes.Buffer
	for i := range n.Lines().Len() {
		seg := n.Lines().At(i)
		b.Write(seg.Value(source))
	}
	if n.HasClosure() {
		cl := n.ClosureLine
		b.Write(cl.Value(source))
	}
	w.WriteString(`<p style="margin:0">`)
	w.WriteString(strings.ReplaceAll(html.EscapeString(strings.TrimRight(b.String(), "\n")), "\n", "<br>"))
	w.WriteString("</p>\n")
	return ast.WalkSkipChildren, nil
}

func (r *qtRenderer) rawHTML(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.RawHTML)
	for i := range n.Segments.Len() {
		seg := n.Segments.At(i)
		w.WriteString(html.EscapeString(string(seg.Value(source))))
	}
	return ast.WalkSkipChildren, nil
}

// image: картинки по ссылкам не грузим — показываем ссылкой.
func (r *qtRenderer) image(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.Image)
	var alt bytes.Buffer
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			alt.Write(t.Segment.Value(source))
		}
	}
	label := alt.String()
	if label == "" {
		label = string(n.Destination)
	}
	w.WriteString(`<a href="` + html.EscapeString(string(util.URLEscape(n.Destination, true))) + `">🖼 ` + html.EscapeString(label) + `</a>`)
	return ast.WalkSkipChildren, nil
}

// blockquote — полоса 3 px отдельной ячейкой: рамки у абзаца Qt не рисует.
func (r *qtRenderer) blockquote(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
	p := r.o.Palette
	if entering {
		fmt.Fprintf(w, `<table cellspacing="0" cellpadding="0" style="margin-top:4px;margin-bottom:4px"><tr><td width="3" bgcolor="%s"></td><td style="padding-left:10px;padding-right:10px;color:%s">`+"\n", p.Line2, p.Tx2)
	} else {
		w.WriteString("</td></tr></table>\n")
	}
	return ast.WalkContinue, nil
}

// table — сетка 1 px из фона таблицы, просвечивающего через cellspacing:
// border-collapse в Qt 6.8 прячет рамки целиком.
func (r *qtRenderer) table(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		fmt.Fprintf(w, `<table cellspacing="1" cellpadding="3" bgcolor="%s" style="margin-top:4px;margin-bottom:4px">`+"\n", r.o.Palette.Line)
	} else {
		w.WriteString("</table>\n")
	}
	return ast.WalkContinue, nil
}

func (r *qtRenderer) tableCell(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*east.TableCell)
	tag, bg := "td", r.o.Palette.Surf
	if n.Parent().Kind() == east.KindTableHeader {
		tag, bg = "th", r.o.Palette.Surf2
	}
	if !entering {
		w.WriteString("</" + tag + ">\n")
		return ast.WalkContinue, nil
	}
	align := "left"
	switch n.Alignment {
	case east.AlignCenter:
		align = "center"
	case east.AlignRight:
		align = "right"
	}
	fmt.Fprintf(w, `<%s bgcolor="%s" align="%s">`, tag, bg, align)
	return ast.WalkContinue, nil
}

// heading — абзацем со своим кеглем: у <h1>…<h3> в Qt слишком крупные поля.
func (r *qtRenderer) heading(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		w.WriteString("</p>\n")
		return ast.WalkContinue, nil
	}
	size, top, bottom := 15, 4, 2
	switch node.(*ast.Heading).Level {
	case 1:
		size, top, bottom = 20, 8, 4
	case 2:
		size, top, bottom = 17, 6, 4
	}
	fmt.Fprintf(w, `<p style="font-size:%dpx;font-weight:600;margin-top:%dpx;margin-bottom:%dpx">`, size, top, bottom)
	return ast.WalkContinue, nil
}

func (r *qtRenderer) taskCheckBox(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		if node.(*east.TaskCheckBox).IsChecked {
			w.WriteString("☑ ")
		} else {
			w.WriteString("☐ ")
		}
	}
	return ast.WalkContinue, nil
}
