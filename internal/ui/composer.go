package ui

import (
	"strings"

	qt "github.com/mappu/miqt/qt6"
)

// Composer — поле ввода: Enter отправляет, Shift+Enter переносит строку.
type Composer struct {
	edit *qt.QPlainTextEdit

	OnSend func(text string)
}

func NewComposer() *Composer {
	c := &Composer{edit: qt.NewQPlainTextEdit2()}
	c.edit.SetPlaceholderText("Сообщение… Enter — отправить, Shift+Enter — новая строка")
	c.edit.SetFixedHeight(64)
	c.edit.OnKeyPressEvent(func(super func(*qt.QKeyEvent), e *qt.QKeyEvent) {
		k := qt.Key(e.Key())
		if (k == qt.Key_Return || k == qt.Key_Enter) && e.Modifiers()&qt.ShiftModifier == 0 {
			c.send()
			return
		}
		super(e)
	})
	return c
}

func (c *Composer) Widget() *qt.QWidget { return c.edit.QWidget }

func (c *Composer) send() {
	text := strings.TrimSpace(c.edit.ToPlainText())
	if text == "" || c.OnSend == nil {
		return
	}
	c.edit.Clear()
	c.OnSend(text)
}

// Restore возвращает неотправленный текст, если пользователь ещё не начал новый.
func (c *Composer) Restore(text string) {
	if strings.TrimSpace(c.edit.ToPlainText()) == "" {
		c.edit.SetPlainText(text)
	}
}

func (c *Composer) SetEnabled(on bool) { c.edit.SetEnabled(on) }

func (c *Composer) Focus() { c.edit.SetFocus() }
