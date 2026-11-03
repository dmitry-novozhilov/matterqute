package ui

import (
	"fmt"
	"html"
	"strings"

	qt "github.com/mappu/miqt/qt6"
)

// Окно «Сочетания клавиш» (Ctrl+/, «Справка → Сочетания клавиш»), как в
// веб-клиенте. Сочетания — в записи Qt («Ctrl+Shift+K», «Alt+Up»): тест
// сверяет список с действиями окна и поля ввода, на экране стрелки — значками.

type shortcutRow struct{ keys, what string }

type shortcutGroup struct {
	title string
	rows  []shortcutRow
}

var shortcutGroups = []shortcutGroup{
	{"Каналы и переходы", []shortcutRow{
		{"Ctrl+K", "Перейти к каналу, личке или человеку"},
		{"Alt+Up / Alt+Down", "Предыдущий или следующий канал в списке"},
		{"Alt+Shift+Up / Alt+Shift+Down", "Предыдущий или следующий канал с новыми сообщениями"},
		{"Ctrl+Shift+K", "Новое личное сообщение"},
		{"Ctrl+F", "Поиск по сообщениям"},
		{"Ctrl+Shift+M", "Упоминания вас"},
		{"Ctrl+1 … Ctrl+9", "Сервер по номеру в меню «Сервер»"},
	}},
	{"Поле ввода", []shortcutRow{
		{"Enter", "Отправить (в блоке кода — новая строка)"},
		{"Shift+Enter", "Новая строка (в списке — пункт, в цитате — строка цитаты)"},
		{"Ctrl+Enter", "Отправить всегда"},
		{"Ctrl+Shift+Enter", "Отправить позже…"},
		{"Up", "В пустом поле — изменить своё последнее сообщение"},
		{"Esc", "Отменить правку сообщения"},
		{"Tab / Shift+Tab", "Уровень пункта списка"},
		{"Ctrl+B", "Жирный"},
		{"Ctrl+I", "Курсив"},
		{"Ctrl+Shift+X", "Зачёркнутый"},
		{"Ctrl+Shift+C", "Код"},
		{"Ctrl+Alt+K", "Ссылка"},
		{"Ctrl+Shift+8", "Список"},
		{"Ctrl+Shift+7", "Нумерованный список"},
		{"Ctrl+Shift+9", "Цитата"},
		{"Ctrl+Alt+Shift+C", "Блок кода"},
		{"Ctrl+Z", "Отменить (и превращение набранного markdown в оформление)"},
	}},
	{"Лента", []shortcutRow{
		{"Ctrl+C", "Копировать выделенное"},
		{"Ctrl+A", "Выделить всё"},
	}},
	{"Просмотр картинки", []shortcutRow{
		{"Left / Right", "Предыдущая или следующая картинка сообщения"},
		{"+ / −", "Крупнее или мельче"},
		{"1", "В натуральную величину"},
		{"0", "По размеру экрана"},
		{"Esc", "Закрыть"},
	}},
	{"Приложение", []shortcutRow{
		{"Ctrl+= / Ctrl+-", "Масштаб: крупнее или мельче (лента, поле ввода, сайдбар)"},
		{"Ctrl+0", "Обычный масштаб"},
		{"Ctrl+/", "Сочетания клавиш"},
		{"Ctrl+Q", "Выход"},
	}},
}

// keysHTML — сочетание для показа: клавиши — плашками, стрелки — значками.
func keysHTML(keys string) string {
	pretty := strings.NewReplacer("Up", "↑", "Down", "↓", "Left", "←", "Right", "→").Replace(keys)
	return html.EscapeString(pretty)
}

func shortcutsHTML() string {
	pal := Pal()
	var b strings.Builder
	for _, g := range shortcutGroups {
		fmt.Fprintf(&b, `<p style="margin-top:14px;margin-bottom:4px"><span style="font-weight:600">%s</span></p>`, html.EscapeString(g.title))
		b.WriteString(`<table cellspacing="0" cellpadding="3">`)
		for _, r := range g.rows {
			fmt.Fprintf(&b, `<tr><td width="230"><span style="font-family:'PT Mono';color:%s">%s</span></td><td>%s</td></tr>`,
				pal.Tx, keysHTML(r.keys), html.EscapeString(r.what))
		}
		b.WriteString(`</table>`)
	}
	return b.String()
}

// showShortcuts — окно сочетаний (открытое — поверх).
func (s *Shell) showShortcuts() {
	if w := s.shortcuts; w != nil {
		w.Raise()
		w.ActivateWindow()
		return
	}
	w := qt.NewQWidget3(s.w.QWidget, qt.Window) // с родителем: закроется вместе с главным
	w.SetAttribute(qt.WA_DeleteOnClose)
	w.SetWindowTitle("Сочетания клавиш")
	w.Resize(620, 720)
	view := qt.NewQTextBrowser2()
	view.SetFrameShape(qt.QFrame__NoFrame)
	view.SetHtml(shortcutsHTML())
	lay := qt.NewQVBoxLayout(w)
	lay.SetContentsMargins(16, 8, 16, 16)
	lay.AddWidget(view.QWidget)
	w.OnDestroyed(func() { s.shortcuts = nil })
	s.shortcuts = w
	w.Show()
}
