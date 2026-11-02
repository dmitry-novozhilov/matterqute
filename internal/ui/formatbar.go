package ui

import (
	"fmt"
	"net/url"
	"strings"

	qt "github.com/mappu/miqt/qt6"

	"matterqute/internal/markdown"
)

// formatActions — оформление в поле ввода: кнопки панели и сочетания клавиш.
type formatActions struct {
	bold, italic, strike, code, link  *qt.QAction
	bullet, ordered, quote, codeBlock *qt.QAction
	inline                            []*qt.QAction // в блоке кода недоступны
	icons                             map[*qt.QAction]string
	linkDialog                        *dialog
}

// SetFormatBar — показывать ли панель оформления над полем ввода (во всех
// полях сразу, как шрифты).
func SetFormatBar(on bool) {
	style.formatBar = on
	for _, f := range style.listeners {
		f()
	}
}

// OnFormatBarChosen — панель показали или спрятали кнопкой «Aa» (сохранить).
var OnFormatBarChosen func(on bool)

func (c *Composer) buildFormatBar() {
	a := &c.acts
	a.icons = map[*qt.QAction]string{}
	act := func(iconName, tip, keys string, do func()) *qt.QAction {
		x := qt.NewQAction5(tip, c.edit.QObject)
		x.SetShortcut(qt.NewQKeySequence2(keys))
		x.SetShortcutContext(qt.WidgetShortcut)
		x.SetToolTip(tip + " (" + keys + ")")
		x.SetCheckable(true)
		x.OnTriggered(func() {
			do()
			c.syncFormat()
			c.edit.SetFocus()
		})
		c.edit.AddAction(x)
		a.icons[x] = iconName
		return x
	}
	inline := func(get func(markdown.Span) bool, set func(*markdown.Span, bool)) func() {
		return func() { c.toggleInline(get, set) }
	}
	a.bold = act("bold", "Жирный", "Ctrl+B", inline(func(s markdown.Span) bool { return s.Bold }, func(s *markdown.Span, on bool) { s.Bold = on }))
	a.italic = act("italic", "Курсив", "Ctrl+I", inline(func(s markdown.Span) bool { return s.Italic }, func(s *markdown.Span, on bool) { s.Italic = on }))
	a.strike = act("strikethrough", "Зачёркнутый", "Ctrl+Shift+X", inline(func(s markdown.Span) bool { return s.Strike }, func(s *markdown.Span, on bool) { s.Strike = on }))
	a.code = act("code", "Код", "Ctrl+Shift+C", inline(func(s markdown.Span) bool { return s.Code }, func(s *markdown.Span, on bool) { s.Code = on }))
	a.link = act("link", "Ссылка", "Ctrl+Alt+K", c.editLink) // Ctrl+K — быстрый переход, как в веб-клиенте
	a.bullet = act("list", "Список", "Ctrl+Shift+8", func() { c.toggleBlock(markdown.Bullet) })
	a.ordered = act("list-ordered", "Нумерованный список", "Ctrl+Shift+7", func() { c.toggleBlock(markdown.Ordered) })
	a.quote = act("text-quote", "Цитата", "Ctrl+Shift+9", func() { c.toggleBlock(markdown.Quote) })
	a.codeBlock = act("square-code", "Блок кода", "Ctrl+Alt+Shift+C", func() { c.toggleBlock(markdown.Code) })
	a.inline = []*qt.QAction{a.bold, a.italic, a.strike, a.code, a.link}

	c.fmtBar = qt.NewQWidget2()
	setName(c.fmtBar.QObject, "formatBar")
	lay := qt.NewQHBoxLayout(c.fmtBar)
	lay.SetContentsMargins(0, 0, 0, 0)
	lay.SetSpacing(2)
	for i, group := range [][]*qt.QAction{{a.bold, a.italic, a.strike}, {a.code, a.link}, {a.bullet, a.ordered, a.quote, a.codeBlock}} {
		if i > 0 {
			sep := qt.NewQFrame2()
			setName(sep.QObject, "formatSep")
			sep.SetFixedSize2(1, 16)
			lay.AddSpacing(4)
			lay.AddWidget(sep.QWidget)
			lay.AddSpacing(4)
		}
		for _, x := range group {
			b := qt.NewQToolButton2()
			b.SetDefaultAction(x)
			b.SetFixedSize2(28, 28)
			b.SetIconSize(qt.NewQSize2(16, 16))
			b.SetFocusPolicy(qt.NoFocus)
			lay.AddWidget(b.QWidget)
		}
	}
	lay.AddStretch()

	c.fmtShow = qt.NewQToolButton2()
	setName(c.fmtShow.QObject, "formatShow")
	c.fmtShow.SetCheckable(true)
	c.fmtShow.SetFixedSize2(32, 32)
	c.fmtShow.SetIconSize(qt.NewQSize2(18, 18))
	c.fmtShow.SetFocusPolicy(qt.NoFocus)
	c.fmtShow.SetToolTip("Оформление текста")
	c.fmtShow.OnToggled(func(on bool) {
		if on == style.formatBar {
			return
		}
		SetFormatBar(on)
		if OnFormatBarChosen != nil {
			OnFormatBarChosen(on)
		}
		c.edit.SetFocus()
	})
}

func (c *Composer) restyleFormatBar() {
	pal := Pal()
	for x, name := range c.acts.icons {
		x.SetIcon(icon(name, pal.Tx2, 16))
	}
	c.fmtShow.SetIcon(icon("type", pal.Tx2, 18))
	css := fmt.Sprintf(`QToolButton { border: none; border-radius: 4px; background: transparent; min-width: 0; min-height: 0; }
QToolButton:hover { background: %[1]s; }
QToolButton:checked { background: %[2]s; }
QToolButton:disabled { background: transparent; }
QFrame#formatSep { background: %[3]s; }`, pal.Hover, pal.SelBg, pal.Line)
	c.fmtBar.SetStyleSheet(css)
	c.fmtShow.SetStyleSheet(css)
	c.fmtBar.SetVisible(style.formatBar)
	c.fmtShow.SetChecked(style.formatBar)
}

// syncFormat — кнопки панели показывают оформление у курсора.
func (c *Composer) syncFormat() {
	a := &c.acts
	if a.bold == nil {
		return
	}
	sp := spanOf(c.edit.CurrentCharFormat())
	kind := blockKind(c.edit.TextCursor().Block())
	for x, on := range map[*qt.QAction]bool{a.bold: sp.Bold, a.italic: sp.Italic, a.strike: sp.Strike, a.code: sp.Code, a.link: sp.Link != "",
		a.bullet: kind == markdown.Bullet, a.ordered: kind == markdown.Ordered, a.quote: kind == markdown.Quote, a.codeBlock: kind == markdown.Code} {
		x.SetChecked(on)
	}
	for _, x := range a.inline {
		x.SetEnabled(kind != markdown.Code)
	}
}

// editLink — «Ссылка» (Ctrl+Alt+K): текст и адрес; внутри ссылки — она целиком,
// с кнопкой «Убрать ссылку».
func (c *Composer) editLink() {
	cur := c.edit.TextCursor()
	sp := spanOf(c.edit.CurrentCharFormat())
	if !cur.HasSelection() && sp.Link != "" {
		selectLink(cur, sp.Link)
		c.edit.SetTextCursor(cur)
	}
	selected := strings.NewReplacer(" ", " ", " ", " ").Replace(cur.SelectedText())
	d := newDialog(c.w, "Ссылка")
	c.acts.linkDialog = d
	text := qt.NewQLineEdit3(selected)
	addr := qt.NewQLineEdit3(sp.Link)
	addr.SetPlaceholderText("https://")
	d.lay.AddWidget(fieldLabel("Текст").QWidget)
	d.lay.AddWidget(text.QWidget)
	d.lay.AddSpacing(6)
	d.lay.AddWidget(fieldLabel("Адрес").QWidget)
	d.lay.AddWidget(addr.QWidget)
	if sp.Link != "" {
		d.extra("Убрать ссылку", func() {
			c.restyleSelection(func(s *markdown.Span) { s.Link = "" })
			c.edit.SetFocus()
		})
	}
	d.buttons("Готово", func() { c.applyLink(cur, selected, text.Text(), addr.Text()) })
	d.w.Show()
	if selected == "" || sp.Link != "" {
		addr.SetFocus()
	}
}

// applyLink — ссылка на адрес href с текстом text на месте выделения
// (было selected) или в позиции курсора.
func (c *Composer) applyLink(cur *qt.QTextCursor, selected, text, href string) {
	href = linkTarget(href)
	c.edit.SetFocus()
	if href == "" {
		return
	}
	if text == "" {
		text = href
	}
	c.edit.SetTextCursor(cur)
	if cur.HasSelection() && text == selected {
		c.restyleSelection(func(s *markdown.Span) { s.Link = href })
		return
	}
	typing := spanOf(c.edit.CurrentCharFormat())
	sp := typing
	sp.Link, sp.Code = href, false
	cur.InsertText2(text, spanFormat(sp))
	c.edit.SetTextCursor(cur)
	typing.Link = ""
	c.edit.SetCurrentCharFormat(spanFormat(typing)) // дальше — без ссылки
}

// linkTarget — адрес ссылки: без схемы — https:// (или mailto: для почты).
func linkTarget(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if u, err := url.Parse(s); err == nil && u.Scheme != "" && (u.Host != "" || u.Scheme == "mailto") {
		return s
	}
	if strings.Contains(s, "@") && !strings.Contains(s, "/") {
		return "mailto:" + s
	}
	return "https://" + s
}

// selectLink выделяет курсором всю ссылку href вокруг него (в пределах абзаца).
func selectLink(cur *qt.QTextCursor, href string) {
	type frag struct {
		start, end int
		link       bool
	}
	var frags []frag
	for it := cur.Block().Begin(); !it.AtEnd(); it.OperatorPlusPlus() {
		fr := it.Fragment()
		cf := fr.CharFormat()
		frags = append(frags, frag{fr.Position(), fr.Position() + fr.Length(), cf.IsAnchor() && cf.AnchorHref() == href})
	}
	pos := cur.Position()
	from, to := pos, pos
	for i, f := range frags {
		if !f.link || pos < f.start || pos > f.end {
			continue
		}
		from, to = f.start, f.end
		for j := i - 1; j >= 0 && frags[j].link; j-- {
			from = frags[j].start
		}
		for j := i + 1; j < len(frags) && frags[j].link; j++ {
			to = frags[j].end
		}
		break
	}
	cur.SetPosition(from)
	cur.SetPosition2(to, qt.QTextCursor__KeepAnchor)
}

// Скан-коды клавиш верхнего ряда «7», «8», «9» (X11 и Wayland: evdev + 8).
const scan7, scan8, scan9 = 16, 17, 18

// blockShortcut — Ctrl+Shift+7/8/9 (списки, цитата) по скан-коду. Qt
// сочетание «Ctrl+Shift+8» с нажатием не сопоставлял: с Shift клавиша даёт
// другой символ («*», на русской раскладке у «7» — «?»), и действие не
// срабатывало. Скан-код от раскладки не зависит. false — не наше сочетание.
func (c *Composer) blockShortcut(e *qt.QKeyEvent) bool {
	var a *qt.QAction
	switch e.NativeScanCode() {
	case scan7:
		a = c.acts.ordered
	case scan8:
		a = c.acts.bullet
	case scan9:
		a = c.acts.quote
	}
	if a == nil {
		return false
	}
	a.Trigger()
	return true
}
