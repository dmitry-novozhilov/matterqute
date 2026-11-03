package ui

import (
	"regexp"
	"strings"
	"unicode/utf16"

	qt "github.com/mappu/miqt/qt6"

	"matterqute/internal/markdown"
)

// Поле ввода с оформлением (WYSIWYG): жирный, курсив, зачёркнутый, код,
// ссылки, списки, цитаты, блоки кода — на экране без markdown-символов.
// Документ поля читается абзацами (readBlocks) и уходит на сервер markdown'ом
// (markdown.FromBlocks); черновики хранятся теми же абзацами и рисуются заново
// (writeBlocks) — так же при смене темы и шрифтов. Форматы в документе всегда
// такие, какие дают spanFormat и blockFormat: их и только их понимает readBlocks.

const (
	propCode      = int(qt.QTextFormat__UserProperty) + 1 // символы: код
	propQuote     = int(qt.QTextFormat__UserProperty) + 2 // абзац: цитата
	propCodeBlock = int(qt.QTextFormat__UserProperty) + 3 // абзац: блок кода
	boldWeight    = 600                                   // Plex SemiBold — самый жирный из зашитых
	quoteIndent   = 14                                    // отступ цитаты: слева от текста — полоса
)

// readBlocks — документ поля абзацами.
func readBlocks(doc *qt.QTextDocument) []markdown.Block {
	var out []markdown.Block
	for b := doc.FirstBlock(); b.IsValid(); b = b.Next() {
		blk := markdown.Block{Kind: blockKind(b)}
		if blk.Kind == markdown.Code {
			blk.Spans = []markdown.Span{{Text: b.Text()}}
			out = append(out, blk)
			continue
		}
		if l := b.TextList(); l != nil {
			blk.Level = max(0, l.Format().Indent()-1)
		}
		for it := b.Begin(); !it.AtEnd(); it.OperatorPlusPlus() {
			fr := it.Fragment()
			sp := spanOf(fr.CharFormat())
			sp.Text = strings.ReplaceAll(fr.Text(), "￼", "") // картинок в поле нет
			if n := len(blk.Spans); n > 0 && sameStyle(blk.Spans[n-1], sp) {
				blk.Spans[n-1].Text += sp.Text
			} else if sp.Text != "" {
				blk.Spans = append(blk.Spans, sp)
			}
		}
		out = append(out, blk)
	}
	return out
}

func blockKind(b *qt.QTextBlock) markdown.BlockKind {
	bf := b.BlockFormat()
	switch l := b.TextList(); {
	case bf.BoolProperty(propCodeBlock):
		return markdown.Code
	case l != nil:
		if listOrdered(l.Format().Style()) {
			return markdown.Ordered
		}
		return markdown.Bullet
	case bf.BoolProperty(propQuote):
		return markdown.Quote
	}
	return markdown.Para
}

func listOrdered(s qt.QTextListFormat__Style) bool {
	return s <= qt.QTextListFormat__ListDecimal && s != qt.QTextListFormat__ListStyleUndefined
}

func spanOf(cf *qt.QTextCharFormat) markdown.Span {
	sp := markdown.Span{Bold: cf.FontWeight() >= boldWeight, Italic: cf.FontItalic(), Strike: cf.FontStrikeOut(), Code: cf.BoolProperty(propCode)}
	if cf.IsAnchor() {
		sp.Link = cf.AnchorHref()
	}
	return sp
}

func sameStyle(a, b markdown.Span) bool {
	a.Text, b.Text = "", ""
	return a == b
}

// spanFormat — формат символов для оформления sp в текущей теме.
func spanFormat(sp markdown.Span) *qt.QTextCharFormat {
	cf := qt.NewQTextCharFormat()
	pal := Pal()
	if sp.Bold {
		cf.SetFontWeight(boldWeight)
	}
	if sp.Italic {
		cf.SetFontItalic(true)
	}
	if sp.Strike {
		cf.SetFontStrikeOut(true)
	}
	if sp.Code {
		cf.SetFontFamilies([]string{monoFamily()})
		cf.SetBackground(qt.NewQBrush3(qt.NewQColor6(pal.Surf2)))
		cf.SetProperty(propCode, qt.NewQVariant8(true))
	}
	if sp.Link != "" {
		cf.SetAnchor(true)
		cf.SetAnchorHref(sp.Link)
		cf.SetForeground(qt.NewQBrush3(qt.NewQColor6(pal.Acc)))
		cf.SetFontUnderline(true)
	}
	return cf
}

// blockFormat — формат абзаца вида kind (списки — отдельно, QTextList).
func blockFormat(kind markdown.BlockKind) *qt.QTextBlockFormat {
	bf := qt.NewQTextBlockFormat()
	switch kind {
	case markdown.Quote:
		bf.SetProperty(propQuote, qt.NewQVariant8(true))
		bf.SetLeftMargin(quoteIndent)
	case markdown.Code:
		bf.SetProperty(propCodeBlock, qt.NewQVariant8(true))
		bf.SetBackground(qt.NewQBrush3(qt.NewQColor6(Pal().Surf2)))
		bf.SetLeftMargin(4)
		bf.SetRightMargin(4)
	}
	return bf
}

// blockCharFormat — формат символов абзаца: в блоке кода — моноширинный.
func blockCharFormat(kind markdown.BlockKind) *qt.QTextCharFormat {
	cf := qt.NewQTextCharFormat()
	if kind == markdown.Code {
		cf.SetFontFamilies([]string{monoFamily()})
	}
	return cf
}

func listFormat(kind markdown.BlockKind, level int) *qt.QTextListFormat {
	lf := qt.NewQTextListFormat()
	styles := []qt.QTextListFormat__Style{qt.QTextListFormat__ListDisc, qt.QTextListFormat__ListCircle, qt.QTextListFormat__ListSquare}
	if kind == markdown.Ordered {
		lf.SetStyle(qt.QTextListFormat__ListDecimal)
	} else {
		lf.SetStyle(styles[level%len(styles)])
	}
	lf.SetIndent(level + 1)
	return lf
}

// writeBlocks вставляет абзацы с позиции курсора (в пустой документ).
func writeBlocks(cur *qt.QTextCursor, blocks []markdown.Block) {
	var lists []*qt.QTextList // открытые списки по уровням
	var kinds []markdown.BlockKind
	for i, b := range blocks {
		if i > 0 {
			cur.InsertBlock2(blockFormat(b.Kind), blockCharFormat(b.Kind))
		} else {
			cur.SetBlockFormat(blockFormat(b.Kind))
			cur.SetBlockCharFormat(blockCharFormat(b.Kind))
		}
		switch b.Kind {
		case markdown.Bullet, markdown.Ordered:
			level := min(max(b.Level, 0), len(lists))
			keep := min(level+1, len(lists))
			if level < keep && kinds[level] != b.Kind {
				keep = level
			}
			lists, kinds = lists[:keep], kinds[:keep]
			if level < len(lists) {
				lists[level].Add(cur.Block())
			} else {
				lists = append(lists, cur.CreateList(listFormat(b.Kind, level)))
				kinds = append(kinds, b.Kind)
			}
		default:
			lists, kinds = nil, nil
		}
		if b.Kind == markdown.Code {
			cur.InsertText2(b.Text(), blockCharFormat(markdown.Code))
			continue
		}
		for _, sp := range b.Spans {
			cur.InsertText2(sp.Text, spanFormat(sp))
		}
	}
}

func blocksEmpty(blocks []markdown.Block) bool {
	for _, b := range blocks {
		if strings.TrimSpace(b.Text()) != "" {
			return false
		}
	}
	return true
}

// selectedBlocks — абзацы, которых касается выделение (или абзац курсора).
func selectedBlocks(doc *qt.QTextDocument, cur *qt.QTextCursor) []*qt.QTextBlock {
	start, end := cur.SelectionStart(), cur.SelectionEnd()
	var out []*qt.QTextBlock
	for b := doc.FindBlock(start); b.IsValid() && (b.Position() <= end); b = b.Next() {
		out = append(out, b)
		if b.Position()+b.Length() > end {
			break
		}
	}
	return out
}

// restyleSelection меняет оформление выделенного, без выделения — того, что
// будет набрано: каждый кусок перечитывается в Span, меняется change и
// пишется заново. В блоках кода оформления нет — их не трогает.
func (c *Composer) restyleSelection(change func(*markdown.Span)) {
	cur := c.edit.TextCursor()
	if !cur.HasSelection() {
		sp := spanOf(c.edit.CurrentCharFormat())
		change(&sp)
		c.edit.SetCurrentCharFormat(spanFormat(sp))
		return
	}
	type piece struct {
		from, to int
		sp       markdown.Span
	}
	var pieces []piece
	start, end := cur.SelectionStart(), cur.SelectionEnd()
	for _, b := range selectedBlocks(c.edit.Document(), cur) {
		if blockKind(b) == markdown.Code {
			continue
		}
		for it := b.Begin(); !it.AtEnd(); it.OperatorPlusPlus() {
			fr := it.Fragment()
			from, to := max(fr.Position(), start), min(fr.Position()+fr.Length(), end)
			if from < to {
				sp := spanOf(fr.CharFormat())
				change(&sp)
				pieces = append(pieces, piece{from, to, sp})
			}
		}
	}
	w := qt.NewQTextCursor2(c.edit.Document())
	w.BeginEditBlock()
	for _, p := range pieces {
		w.SetPosition(p.from)
		w.SetPosition2(p.to, qt.QTextCursor__KeepAnchor)
		w.SetCharFormat(spanFormat(p.sp))
	}
	w.EndEditBlock()
}

// selectionSpans — оформление выделенного (без выделения — набираемого).
func (c *Composer) selectionSpans() []markdown.Span {
	cur := c.edit.TextCursor()
	if !cur.HasSelection() {
		return []markdown.Span{spanOf(c.edit.CurrentCharFormat())}
	}
	start, end := cur.SelectionStart(), cur.SelectionEnd()
	var out []markdown.Span
	for _, b := range selectedBlocks(c.edit.Document(), cur) {
		if blockKind(b) == markdown.Code {
			continue
		}
		for it := b.Begin(); !it.AtEnd(); it.OperatorPlusPlus() {
			if fr := it.Fragment(); max(fr.Position(), start) < min(fr.Position()+fr.Length(), end) {
				out = append(out, spanOf(fr.CharFormat()))
			}
		}
	}
	return out
}

// toggleInline — включить оформление, если оно есть не у всего выделенного,
// иначе снять.
func (c *Composer) toggleInline(get func(markdown.Span) bool, set func(*markdown.Span, bool)) {
	on := false
	for _, sp := range c.selectionSpans() {
		if !get(sp) {
			on = true
		}
	}
	c.restyleSelection(func(sp *markdown.Span) { set(sp, on) })
	c.syncFormat()
}

// setBlockKind делает абзацы выделения абзацами вида kind (списки — с уровня
// 0); символы в блоке кода — моноширинные, вне его — как были, но без моно.
func (c *Composer) setBlockKind(cur *qt.QTextCursor, kind markdown.BlockKind) {
	doc := c.edit.Document()
	cur.BeginEditBlock()
	defer cur.EndEditBlock()
	var list *qt.QTextList
	for _, b := range selectedBlocks(doc, cur) {
		was := blockKind(b)
		w := qt.NewQTextCursor4(b)
		if l := b.TextList(); l != nil {
			l.Remove(b)
		}
		w.SetBlockFormat(blockFormat(kind))
		w.SetBlockCharFormat(blockCharFormat(kind))
		if (was == markdown.Code) != (kind == markdown.Code) {
			// Текст абзаца: в блок кода — без оформления, из блока — обычный.
			w.MovePosition2(qt.QTextCursor__EndOfBlock, qt.QTextCursor__KeepAnchor)
			w.SetCharFormat(blockCharFormat(kind))
		}
		if kind == markdown.Bullet || kind == markdown.Ordered {
			if list == nil {
				list = w.CreateList(listFormat(kind, 0))
			} else {
				list.Add(b)
			}
		}
	}
}

// toggleBlock — абзацы выделения становятся видом kind, а если уже все такие —
// обычными строками.
func (c *Composer) toggleBlock(kind markdown.BlockKind) {
	cur := c.edit.TextCursor()
	all := true
	for _, b := range selectedBlocks(c.edit.Document(), cur) {
		if blockKind(b) != kind {
			all = false
		}
	}
	if all {
		kind = markdown.Para
	}
	c.setBlockKind(cur, kind)
	c.syncFormat()
	c.edit.SetFocus()
}

// indentList — Tab и Shift+Tab в пункте списка: уровень глубже или выше.
// Пункт встаёт в список того же уровня выше по тексту (нумерация
// продолжается), если до него нет пунктов мельче; иначе — в новый.
func (c *Composer) indentList(d int) bool {
	cur := c.edit.TextCursor()
	l := cur.CurrentList()
	if l == nil {
		return false
	}
	b := cur.Block()
	kind := blockKind(b)
	level := l.Format().Indent() - 1 + d
	if level < 0 {
		return true
	}
	cur.BeginEditBlock()
	defer cur.EndEditBlock()
	for p := b.Previous(); p.IsValid(); p = p.Previous() {
		pl := p.TextList()
		if pl == nil || pl.Format().Indent()-1 < level {
			break
		}
		if pl.Format().Indent()-1 == level && blockKind(p) == kind {
			pl.Add(b)
			return true
		}
	}
	l.Remove(b)
	w := qt.NewQTextCursor4(b)
	w.SetBlockFormat(blockFormat(kind))
	w.CreateList(listFormat(kind, level))
	return true
}

// leaveCodeAndLink — курсор перешёл (стрелкой, кликом, из кода) в конец
// кода или ссылки: набор дальше — обычным текстом, а не продолжением их. При
// наборе с клавиатуры не зовётся: включённый код набирается подряд.
func (c *Composer) leaveCodeAndLink() {
	cur := c.edit.TextCursor()
	if cur.HasSelection() || blockKind(cur.Block()) == markdown.Code {
		return
	}
	sp := spanOf(c.edit.CurrentCharFormat())
	if !sp.Code && sp.Link == "" {
		return
	}
	next := markdown.Span{}
	if !cur.AtBlockEnd() {
		n := qt.NewQTextCursor2(c.edit.Document())
		n.SetPosition(cur.Position())
		n.MovePosition2(qt.QTextCursor__NextCharacter, qt.QTextCursor__KeepAnchor)
		next = spanOf(n.CharFormat())
	}
	if sp.Code && next.Code || sp.Link != "" && next.Link == sp.Link {
		return // внутри
	}
	sp.Code, sp.Link = sp.Code && next.Code, ""
	c.edit.SetCurrentCharFormat(spanFormat(sp))
}

// newLine — Shift+Enter (и Enter в блоке кода): новая строка того же вида —
// пункт списка, строка цитаты или кода. Пустая строка такого вида вместо этого
// становится обычной: так из списка, цитаты и кода выходят.
func (c *Composer) newLine() {
	cur := c.edit.TextCursor()
	b := cur.Block()
	kind := blockKind(b)
	last := !b.Next().IsValid() || blockKind(b.Next()) != markdown.Code
	switch {
	case !cur.HasSelection() && b.Text() == "" && kind != markdown.Para && (kind != markdown.Code || last):
		c.setBlockKind(cur, markdown.Para)
	default:
		cur.InsertBlock()
	}
	c.edit.SetTextCursor(cur)
	c.edit.EnsureCursorVisible()
	c.syncFormat()
}

// pasteText — вставка текстом: строки — абзацами того же вида, что абзац
// курсора (строки кода — в блоке кода, цитаты — в цитате, пункты — в списке),
// как при Enter. Qt вставлял первую строку в абзац, а остальные — обычными
// абзацами (шрифт от курсора — моноширинный, но не код).
func (c *Composer) pasteText(text string) {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	cur := c.edit.TextCursor()
	cur.BeginEditBlock() // одна правка для Ctrl+Z
	cur.InsertText(text) // \n — InsertBlock с форматом текущего абзаца
	cur.EndEditBlock()
	c.edit.SetTextCursor(cur)
	c.edit.EnsureCursorVisible()
	c.syncFormat()
}

// Quote — цитата в конец поля: text — строками цитаты (md — разметка
// сообщения: оформление сохраняется; иначе — как есть), курсор — на новой
// строке под ней.
func (c *Composer) Quote(text string, md bool) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return
	}
	lines := strings.Split(text, "\n")
	var blocks []markdown.Block
	if md {
		for i, l := range lines {
			lines[i] = "> " + l
		}
		blocks = markdown.ToBlocks(strings.Join(lines, "\n"))
	} else {
		for _, l := range lines {
			blocks = append(blocks, markdown.Block{Kind: markdown.Quote, Spans: []markdown.Span{{Text: l}}})
		}
	}
	blocks = append(blocks, markdown.Block{Kind: markdown.Para})
	cur := c.edit.TextCursor()
	cur.BeginEditBlock()
	cur.MovePosition(qt.QTextCursor__End)
	// Последняя строка — пустая обычная: цитата встаёт в неё, иначе — ниже.
	if b := cur.Block(); b.Text() != "" || blockKind(b) != markdown.Para {
		cur.InsertBlock2(blockFormat(markdown.Para), blockCharFormat(markdown.Para))
	}
	writeBlocks(cur, blocks)
	cur.EndEditBlock()
	c.edit.SetTextCursor(cur)
	c.edit.EnsureCursorVisible()
	c.edit.SetFocus()
	c.syncFormat()
}

// backspaceAtStart — Backspace в начале цитаты, кода или пункта списка делает
// абзац обычным (а не склеивает его с предыдущим).
func (c *Composer) backspaceAtStart() bool {
	cur := c.edit.TextCursor()
	if cur.HasSelection() || !cur.AtBlockStart() || blockKind(cur.Block()) == markdown.Para {
		return false
	}
	c.setBlockKind(cur, markdown.Para)
	c.syncFormat()
	return true
}

// Автозамена markdown при наборе: **жирный**, *курсив*, ~~зачёркнутый~~,
// `код` — по закрывающему символу; «- », «1. », «> » в начале строки —
// список и цитата; «```» в начале строки — блок кода. Ctrl+Z возвращает
// набранное как было.
var inlineMarks = []struct {
	re    *regexp.Regexp
	mark  int // длина маркера
	apply func(*markdown.Span)
}{
	{regexp.MustCompile(`(?:^|[^*\pL\pN])(\*\*([^*\s](?:[^*]*[^*\s])?)\*\*)$`), 2, func(s *markdown.Span) { s.Bold = true }},
	{regexp.MustCompile(`(?:^|[^_\pL\pN])(__([^_\s](?:[^_]*[^_\s])?)__)$`), 2, func(s *markdown.Span) { s.Bold = true }},
	{regexp.MustCompile(`(?:^|[^*\pL\pN])(\*([^*\s](?:[^*]*[^*\s])?)\*)$`), 1, func(s *markdown.Span) { s.Italic = true }},
	{regexp.MustCompile(`(?:^|[^_\pL\pN])(_([^_\s](?:[^_]*[^_\s])?)_)$`), 1, func(s *markdown.Span) { s.Italic = true }},
	{regexp.MustCompile(`(?:^|[^~])(~~([^~\s](?:[^~]*[^~\s])?)~~)$`), 2, func(s *markdown.Span) { s.Strike = true }},
	{regexp.MustCompile("(?:^|[^`])(`([^`]+)`)$"), 1, func(s *markdown.Span) { s.Code = true }},
}

var blockMarks = map[string]markdown.BlockKind{"- ": markdown.Bullet, "* ": markdown.Bullet, "+ ": markdown.Bullet,
	"1. ": markdown.Ordered, "1) ": markdown.Ordered, "> ": markdown.Quote}

// autoformat — после набора символа typed.
func (c *Composer) autoformat(typed string) {
	if typed == "" || strings.ContainsAny(typed, "\r\n") {
		return
	}
	cur := c.edit.TextCursor()
	b := cur.Block()
	if cur.HasSelection() || blockKind(b) == markdown.Code || spanOf(c.edit.CurrentCharFormat()).Code {
		return
	}
	text := utf16.Encode([]rune(b.Text()))
	pos := cur.PositionInBlock()
	if pos > len(text) {
		return
	}
	before := string(utf16.Decode(text[:pos]))
	start := b.Position()

	if kind, ok := blockMarks[before]; ok && blockKind(b) == markdown.Para {
		cur.BeginEditBlock()
		cur.SetPosition(start)
		cur.SetPosition2(start+pos, qt.QTextCursor__KeepAnchor)
		cur.RemoveSelectedText()
		c.setBlockKind(cur, kind)
		cur.EndEditBlock()
		c.edit.SetTextCursor(cur)
		c.syncFormat()
		return
	}
	if before == "```" && blockKind(b) == markdown.Para {
		cur.BeginEditBlock()
		cur.SetPosition(start)
		cur.SetPosition2(start+pos, qt.QTextCursor__KeepAnchor)
		cur.RemoveSelectedText()
		c.setBlockKind(cur, markdown.Code)
		cur.EndEditBlock()
		c.edit.SetTextCursor(cur)
		c.syncFormat()
		return
	}
	if !strings.ContainsAny(typed, "*_~`") {
		return
	}
	for _, m := range inlineMarks {
		loc := m.re.FindStringSubmatchIndex(before)
		if loc == nil {
			continue
		}
		// Позиции в документе — в UTF-16, как у Qt.
		u16 := func(byteOff int) int { return len(utf16.Encode([]rune(before[:byteOff]))) }
		from, to := start+u16(loc[2]), start+u16(loc[3])
		inner := len(utf16.Encode([]rune(before[loc[4]:loc[5]])))
		typing := spanOf(c.edit.CurrentCharFormat())
		cur.BeginEditBlock()
		// Сначала закрывающий маркер, потом открывающий: позиции не съезжают.
		cur.SetPosition(to - m.mark)
		cur.SetPosition2(to, qt.QTextCursor__KeepAnchor)
		cur.RemoveSelectedText()
		cur.SetPosition(from)
		cur.SetPosition2(from+m.mark, qt.QTextCursor__KeepAnchor)
		cur.RemoveSelectedText()
		cur.SetPosition(from)
		cur.SetPosition2(from+inner, qt.QTextCursor__KeepAnchor)
		c.edit.SetTextCursor(cur)
		c.restyleSelection(m.apply)
		cur.SetPosition(from + inner)
		cur.EndEditBlock()
		c.edit.SetTextCursor(cur)
		c.edit.SetCurrentCharFormat(spanFormat(typing)) // дальше — как набиралось до маркеров
		c.syncFormat()
		return
	}
}

// paintQuotes — полоса слева от строк цитаты (у формата абзаца рамок нет).
func (c *Composer) paintQuotes() {
	doc := c.edit.Document()
	lay := doc.DocumentLayout()
	vp := c.edit.Viewport()
	dy := c.edit.VerticalScrollBar().Value()
	var p *qt.QPainter
	for b := doc.FirstBlock(); b.IsValid(); b = b.Next() {
		if blockKind(b) != markdown.Quote {
			continue
		}
		r := lay.BlockBoundingRect(b)
		top := int(r.Y()) - dy
		if top > vp.Height() || top+int(r.Height()) < 0 {
			continue
		}
		if p == nil {
			p = qt.NewQPainter2(vp.QPaintDevice)
			defer p.End()
		}
		// Прямоугольник абзаца начинается до его отступа: полоса — в отступе.
		p.FillRect5(int(r.X())+2, top, 3, int(r.Height()), qt.NewQColor6(Pal().Line2))
	}
}
