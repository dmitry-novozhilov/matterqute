package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// TestComposerLayout: кнопки поля ввода — рядом под текстом, одного размера и
// на одном уровне, «Отправить» справа и доступна только когда есть что
// отправлять; текст — на всю ширину рамки; поле растёт с текстом до половины
// окна (и ужимается, когда окно стало ниже), без текста — одна строка.
func TestComposerLayout(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		m.w.Resize(900, 600)
		if openTown(t, m) == nil {
			return
		}
		c := m.composer
		settle()
		send := c.sendBtn.Geometry()
		for name, b := range map[string]*qt.QToolButton{"attach": c.attach, "emoji": c.emojiBtn, "priority": c.prioBtn, "later": c.later, "format": c.fmtShow} {
			if g := b.Geometry(); g.Y() != send.Y() || g.Height() != send.Height() || g.Width() != send.Width() || g.X() >= send.X() {
				t.Errorf("%s button %d,%d %dx%d, «send» %d,%d %dx%d", name, g.X(), g.Y(), g.Width(), g.Height(), send.X(), send.Y(), send.Width(), send.Height())
			}
		}
		if e, f := c.edit.Geometry(), c.frame.ContentsRect(); e.Bottom() > send.Y() || e.Width() < f.Width()-10 {
			t.Errorf("edit %d,%d %dx%d: want above the buttons (y %d) and as wide as the frame (%d)", e.X(), e.Y(), e.Width(), e.Height(), send.Y(), f.Width())
		}
		if c.sendBtn.IsEnabled() {
			t.Error("«send» enabled with nothing to send")
		}
		line := qt.NewQFontMetrics(c.edit.Font()).LineSpacing()
		one := c.edit.Height()
		if one > line+10 { // строка и поля документа (4+4), высота строки дробная
			t.Errorf("empty field %d px, want one line (%d)", one, line+8)
		}

		c.edit.SetPlainText("привет")
		if !c.sendBtn.IsEnabled() {
			t.Error("«send» disabled with text")
		}
		c.edit.SetPlainText(strings.Repeat("строка\n", 80))
		settle()
		if h := c.edit.Height(); h <= line*8 {
			t.Errorf("80 lines: edit %d px (line %d): still capped at 7 lines", h, line)
		}
		if h, win := c.w.Height(), m.w.Height(); h > win/2 {
			t.Errorf("80 lines: composer %d px in window %d: taller than half", h, win)
		}
		shot(t, m, "composer-tall") // окно 1400×800 — поле до 400
		m.w.Resize(900, 360)
		settle()
		if h, win := c.w.Height(), m.w.Height(); h > win/2 || c.edit.Height() < line*composerMinCap+8 {
			t.Errorf("window %d: composer %d px, edit %d px (line %d)", win, h, c.edit.Height(), line)
		}
		c.edit.SetPlainText("")
		settle()
		if h := c.edit.Height(); h != one || c.sendBtn.IsEnabled() {
			t.Errorf("empty again: edit %d px (want %d), «send» enabled %v", h, one, c.sendBtn.IsEnabled())
		}

		// При правке «Отправить» — «Сохранить», доступна всегда (стёртый текст — удаление).
		c.StartEditWith("p1", "Редактирование", nil)
		if !c.sendBtn.IsEnabled() || !strings.HasPrefix(c.sendBtn.ToolTip(), "Сохранить") {
			t.Errorf("editing: «send» enabled %v, tip %q", c.sendBtn.IsEnabled(), c.sendBtn.ToolTip())
		}
		c.CancelEdit()
		if c.sendBtn.IsEnabled() || !strings.HasPrefix(c.sendBtn.ToolTip(), "Отправить") {
			t.Errorf("after edit: «send» enabled %v, tip %q", c.sendBtn.IsEnabled(), c.sendBtn.ToolTip())
		}
	})
}
