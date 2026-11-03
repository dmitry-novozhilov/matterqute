package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/markdown"
)

// Поле ввода с оформлением: на экране без markdown-символов, на сервер —
// markdown Mattermost.
func TestComposerFormatting(t *testing.T) {
	onMain(func() {
		c := NewComposer()
		defer c.w.Close()
		defer c.dispose()
		sent := "—"
		c.OnSend = func(text string, _ []string, _ string) { sent = text }
		c.w.Resize(700, 240)
		c.w.Show()
		c.edit.SetFocus()
		settle()

		press := func(k qt.Key, mods qt.KeyboardModifier, text string) {
			ev := qt.NewQKeyEvent3(qt.QEvent__KeyPress, int(k), mods, text)
			qt.QCoreApplication_SendEvent(c.edit.QObject, ev.QEvent)
		}
		typeText := func(s string) {
			for _, r := range s {
				press(qt.Key_unknown, qt.NoModifier, string(r))
			}
		}
		shiftEnter := func() { press(qt.Key_Return, qt.ShiftModifier, "\r") }
		send := func() string {
			sent = "—"
			press(qt.Key_Return, qt.NoModifier, "\r")
			return sent
		}
		check := func(what, got, want string) {
			if got != want {
				t.Errorf("%s:\n got %q\nwant %q", what, got, want)
			}
		}
		selectText := func(from, to int) {
			cur := c.edit.TextCursor()
			cur.SetPosition(from)
			cur.SetPosition2(to, qt.QTextCursor__KeepAnchor)
			c.edit.SetTextCursor(cur)
		}

		// Markdown при наборе превращается в оформление, символы пропадают.
		typeText("это **важно** и *очень* ~~не~~ `код` и snake_case_name")
		check("screen text", c.edit.ToPlainText(), "это важно и очень не код и snake_case_name")
		if b := readBlocks(c.edit.Document()); len(b) != 1 || len(b[0].Spans) < 8 || !b[0].Spans[1].Bold {
			t.Errorf("spans after typing: %+v", b)
		}
		shotWidget(t, c.w, "composer-formatting")
		check("autoformat", send(), "это **важно** и *очень* ~~не~~ `код` и snake_case_name")
		if !c.empty() {
			t.Error("composer not cleared after send")
		}

		// Кнопки: жирный на выделенном слове; без выделения — для набираемого.
		typeText("hello world")
		selectText(6, 11)
		c.acts.bold.Trigger()
		if !c.acts.bold.IsChecked() {
			t.Error("bold button not checked on bold selection")
		}
		cur := c.edit.TextCursor()
		cur.MovePosition(qt.QTextCursor__End)
		c.edit.SetTextCursor(cur)
		if !c.acts.bold.IsChecked() {
			t.Error("typing after bold text is not bold") // как в любом редакторе
		}
		c.acts.bold.Trigger()
		c.acts.italic.Trigger()
		typeText(" ok")
		check("buttons", send(), "hello **world** *ok*")

		// Списки: «- » и «1. » в начале строки, Shift+Enter — следующий пункт,
		// Tab — глубже, Shift+Tab — обратно, пустой пункт — выход из списка.
		typeText("- раз")
		shiftEnter()
		typeText("два")
		shiftEnter()
		press(qt.Key_Tab, qt.NoModifier, "\t")
		typeText("вложенный")
		shiftEnter()
		press(qt.Key_Backtab, qt.ShiftModifier, "")
		typeText("три")
		shiftEnter()
		shiftEnter()
		typeText("после")
		check("bullets", send(), "- раз\n- два\n  - вложенный\n- три\n\nпосле")

		// Пустой пункт списка — без подсказки поверх «1.»; пустая строка — с ней.
		c.SetPlaceholder("Ответить в треде")
		typeText("1. ")
		if ph := c.edit.PlaceholderText(); ph != "" || !c.acts.ordered.IsChecked() {
			t.Errorf("empty list item: placeholder %q, list %v", ph, c.acts.ordered.IsChecked())
		}
		press(qt.Key_Backspace, qt.NoModifier, "")
		if ph := c.edit.PlaceholderText(); ph != "Ответить в треде" {
			t.Errorf("empty line after leaving the list: placeholder %q", ph)
		}
		for _, kind := range []markdown.BlockKind{markdown.Quote, markdown.Code, markdown.Bullet} {
			c.toggleBlock(kind)
			if ph := c.edit.PlaceholderText(); ph != "" {
				t.Errorf("empty %v line: placeholder %q", kind, ph)
			}
			c.toggleBlock(kind)
		}
		if ph := c.edit.PlaceholderText(); ph != "Ответить в треде" {
			t.Errorf("placeholder %q after toggling blocks back", ph)
		}
		typeText("1. первый")
		shiftEnter()
		typeText("второй")
		if c.edit.ToPlainText() != "первый\nвторой" || !c.acts.ordered.IsChecked() {
			t.Errorf("ordered list on screen: %q, button %v", c.edit.ToPlainText(), c.acts.ordered.IsChecked())
		}
		check("ordered", send(), "1. первый\n2. второй")

		// Цитата и выход из неё; Backspace в начале цитаты делает строку обычной.
		typeText("> цитата")
		shiftEnter()
		shiftEnter()
		typeText("ответ")
		check("quote", send(), "> цитата\n\nответ")
		typeText("> нет")
		cur = c.edit.TextCursor()
		cur.MovePosition(qt.QTextCursor__StartOfBlock)
		c.edit.SetTextCursor(cur)
		press(qt.Key_Backspace, qt.NoModifier, "")
		check("backspace leaves quote", send(), "нет")

		// Блок кода: «```», Enter внутри — новая строка, Enter на пустой
		// последней — выход; оформление внутри кода не действует.
		sent = "—"
		typeText("```")
		if !c.acts.codeBlock.IsChecked() || c.acts.bold.IsEnabled() {
			t.Errorf("code block: button %v, bold enabled %v", c.acts.codeBlock.IsChecked(), c.acts.bold.IsEnabled())
		}
		typeText("x := **1**")
		press(qt.Key_Return, qt.NoModifier, "\r")
		typeText("y()")
		press(qt.Key_Return, qt.NoModifier, "\r")
		if sent != "—" {
			t.Errorf("Enter in a code block sent %q", sent)
		}
		press(qt.Key_Return, qt.NoModifier, "\r")
		typeText("готово")
		check("code block", send(), "```\nx := **1**\ny()\n```\nготово")

		// Ссылка через окно: на выделенный текст, адрес без схемы — https.
		typeText("see docs")
		selectText(4, 8)
		c.editLink()
		d := c.acts.linkDialog
		var edits []*qt.QLineEdit
		for _, o := range descendants(d.w.QObject) {
			if o.Inherits("QLineEdit") {
				edits = append(edits, qt.UnsafeNewQLineEdit(o.UnsafePointer()))
			}
		}
		if len(edits) != 2 || edits[0].Text() != "docs" {
			t.Errorf("link dialog fields %d, text %q", len(edits), edits[0].Text())
			return
		}
		edits[1].SetText("go.dev/doc")
		shotWidget(t, d.w.QWidget, "composer-link")
		labelled(d.w.QObject, "Готово").Click()
		check("link", send(), "see [docs](https://go.dev/doc)")

		// Черновик с оформлением переживает переход в другой канал и смену темы.
		typeText("**черновик** и `код`")
		c.SwitchTo("other")
		if !c.empty() {
			t.Error("draft leaked into another channel")
		}
		c.SwitchTo("")
		SetThemeMode(ThemeDark)
		settle()
		blocks := readBlocks(c.edit.Document())
		SetThemeMode(ThemeLight)
		settle()
		if len(blocks) != 1 || !blocks[0].Spans[0].Bold || !blocks[0].Spans[2].Code {
			t.Errorf("draft after switch and theme: %+v", blocks)
		}
		check("draft", send(), "**черновик** и `код`")

		// Панель оформления — кнопкой «T», во всех полях.
		other := NewComposer()
		defer other.w.Close()
		defer other.dispose()
		var chosen []bool
		OnFormatBarChosen = func(on bool) { chosen = append(chosen, on) }
		defer func() { OnFormatBarChosen = nil }()
		c.fmtShow.Click()
		if !c.fmtBar.IsVisibleTo(c.w) || !other.fmtBar.IsVisibleTo(other.w) || len(chosen) != 1 || !chosen[0] {
			t.Errorf("format bar shown: here %v, other %v, chosen %v", c.fmtBar.IsVisibleTo(c.w), other.fmtBar.IsVisibleTo(other.w), chosen)
		}
		typeText("> цитата **жирно**")
		shiftEnter()
		shiftEnter()
		typeText("- пункт с `кодом`")
		shiftEnter()
		press(qt.Key_Tab, qt.NoModifier, "\t")
		typeText("вложенный")
		c.w.Resize(700, c.w.SizeHint().Height())
		shotWidget(t, c.w, "composer-format-bar")
		c.fmtShow.Click()
		if c.fmtBar.IsVisibleTo(c.w) || style.formatBar {
			t.Error("format bar not hidden")
		}
		c.setBlocks(nil)

		// Неудачная отправка возвращает оформленное.
		typeText("**вернуть**")
		send()
		c.Restore()
		if b := readBlocks(c.edit.Document()); len(b) != 1 || !b[0].Spans[0].Bold {
			t.Errorf("restored draft %+v", b)
		}
	})
}

// Оформленное в поле уходит на сервер markdown'ом и рисуется в ленте.
func TestComposerSendsMarkdown(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		SetFormatBar(true)
		defer SetFormatBar(false)
		c := m.composer
		c.setBlocks([]markdown.Block{
			{Kind: markdown.Para, Spans: []markdown.Span{{Text: "Итоги: "}, {Text: "релиз", Bold: true}, {Text: " в "}, {Text: "main", Code: true}}},
			{Kind: markdown.Bullet, Spans: []markdown.Span{{Text: "тесты "}, {Text: "зелёные", Italic: true}}},
			{Kind: markdown.Bullet, Spans: []markdown.Span{{Text: "заметки", Link: "https://example.com/notes"}}},
		})
		shot(t, m, "composer-in-window")
		c.send()
		want := "Итоги: **релиз** в `main`\n- тесты *зелёные*\n- [заметки](https://example.com/notes)"
		if !waitFor(t, "formatted post in the feed", func() bool {
			last := m.feed.items[len(m.feed.items)-1]
			return last.post.Message == want && strings.Contains(last.html, "font-weight:600") && strings.Contains(last.html, `href="https://example.com/notes"`)
		}) {
			t.Logf("last post %q", m.feed.items[len(m.feed.items)-1].post.Message)
			return
		}
		shot(t, m, "composer-sent")
	})
}

// Ctrl+Shift+8/7/9 — по скан-коду: Qt сочетание с нажатием не сопоставлял
// (с Shift клавиша даёт «*», на русской раскладке у «7» — «?»).
func TestBlockShortcutsByScanCode(t *testing.T) {
	onMain(func() {
		c := NewComposer()
		defer c.dispose()
		press := func(key qt.Key, scan uint) {
			e := qt.NewQKeyEvent2(qt.QEvent__KeyPress, int(key), qt.ControlModifier|qt.ShiftModifier, scan, 0, 0)
			qt.QCoreApplication_SendEvent(c.edit.QObject, e.QEvent)
		}
		c.edit.SetPlainText("пункт")
		press(qt.Key_Asterisk, scan8) // английская раскладка: Shift+8 = «*»
		if k := blockKind(c.edit.TextCursor().Block()); k != markdown.Bullet {
			t.Errorf("Ctrl+Shift+8: block %v, want bullet", k)
		}
		press(qt.Key_Question, scan7) // русская: Shift+7 = «?»
		if k := blockKind(c.edit.TextCursor().Block()); k != markdown.Ordered {
			t.Errorf("Ctrl+Shift+7: block %v, want ordered", k)
		}
		press(qt.Key_ParenLeft, scan9)
		if k := blockKind(c.edit.TextCursor().Block()); k != markdown.Quote {
			t.Errorf("Ctrl+Shift+9: block %v, want quote", k)
		}
		if got := c.edit.ToPlainText(); got != "пункт" {
			t.Errorf("text changed: %q", got)
		}
	})
}
