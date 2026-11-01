package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// Автодополнение в поле ввода: @ — пользователи (и те, кого знает только
// сервер), # и ~ — каналы, : — эмодзи; стрелки, Enter, Tab, Esc.
func TestAutocomplete(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		c := m.composer
		c.edit.SetFocus()
		typed := func(text string) {
			c.edit.InsertPlainText(text)
			settle()
		}
		key := func(k qt.Key) {
			ev := qt.NewQKeyEvent(qt.QEvent__KeyPress, int(k), qt.NoModifier)
			qt.QCoreApplication_SendEvent(c.edit.QObject, ev.QEvent)
			settle()
		}
		titles := func() []string {
			if c.popup == nil || !c.popup.visible() {
				return nil
			}
			var out []string
			for _, it := range c.popup.items {
				out = append(out, it.Title)
			}
			return out
		}
		first := func(want string) bool {
			return waitFor(t, "suggestion "+want, func() bool { ts := titles(); return len(ts) > 0 && ts[0] == want })
		}
		text := func() string { return c.edit.ToPlainText() }

		typed("привет @bo")
		if !first("@bob") {
			return
		}
		key(qt.Key_Return)
		if got := text(); got != "привет @bob " {
			t.Errorf("after Enter on @bob: %q", got)
		}
		if titles() != nil {
			t.Error("list still open after a pick")
		}

		// erin клиент не знает — её приносит поиск на сервере.
		if m.st.UserByName("erin") != nil {
			t.Fatal("erin is known before the search: test is meaningless")
		}
		typed("@er")
		if !first("@erin") {
			return
		}
		shot(t, m, "autocomplete-users")
		key(qt.Key_Tab)
		if got := text(); !strings.HasSuffix(got, "@erin ") {
			t.Errorf("after Tab on @erin: %q", got)
		}

		// # — каналы своей команды, вставляется ~имя.
		typed("#tow")
		if !first("Town Square") {
			return
		}
		key(qt.Key_Return)
		if got := text(); !strings.HasSuffix(got, "~town-square ") {
			t.Errorf("after Enter on Town Square: %q", got)
		}

		// : — эмодзи с двух букв; стрелка вниз выбирает следующий.
		typed(":tad")
		if !first(":tada:") {
			return
		}
		key(qt.Key_Return)
		if got := text(); !strings.HasSuffix(got, ":tada: ") {
			t.Errorf("after Enter on :tada: %q", got)
		}
		typed(":par") // кастомное эмодзи сервера
		if !waitFor(t, "custom emoji", func() bool {
			for _, ti := range titles() {
				if ti == ":partyparrot:" {
					return true
				}
			}
			return false
		}) {
			return
		}

		// Esc закрывает и не открывает снова для того же слова; Enter снова отправляет.
		key(qt.Key_Escape)
		if titles() != nil {
			t.Error("Esc did not close the list")
		}
		typed("r")
		settle()
		if titles() != nil {
			t.Error("list reopened for the word dismissed with Esc")
		}
		before := text()
		key(qt.Key_Down)
		if text() != before {
			t.Error("arrow key changed the text while the list was closed")
		}

		// Не подсказываем там, где это не упоминание.
		c.edit.Clear()
		typed("встреча в 10:30 и mail@example")
		settle()
		if titles() != nil {
			t.Errorf("suggestions for plain text: %v", titles())
		}
	})
}
