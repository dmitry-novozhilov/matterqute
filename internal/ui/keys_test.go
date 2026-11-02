package ui

import (
	"io"
	"log/slog"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// Alt+↑/↓ — соседний канал, Alt+Shift+↑/↓ — непрочитанный, по кругу; поле
// ввода сочетания не перехватывает.
func TestChannelKeys(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		order := m.sidebarOrder()
		i := -1
		for j, id := range order {
			if id == m.current {
				i = j
			}
		}
		if i < 0 || order[0] != threadsID {
			t.Errorf("order %v, current %s", order, m.current)
			return
		}
		keys := map[string]*qt.QAction{}
		for _, a := range m.w.Actions() {
			keys[a.Shortcut().ToString()] = a
		}
		press := func(k string) {
			if a := keys[k]; a != nil {
				a.Trigger()
			} else {
				t.Errorf("no action for %s", k)
			}
		}
		press("Alt+Down")
		if m.current != order[(i+1)%len(order)] {
			t.Errorf("Alt+Down: %s, want %s", m.current, order[(i+1)%len(order)])
		}
		press("Alt+Up")
		press("Alt+Up")
		if m.current != order[(i-1+len(order))%len(order)] {
			t.Errorf("Alt+Up twice: %s", m.current)
		}
		// Вверх с «Тредов» — последняя строка (по кругу).
		m.selectChannel(threadsID)
		press("Alt+Up")
		if m.current != order[len(order)-1] {
			t.Errorf("wrap: %s, want %s", m.current, order[len(order)-1])
		}
		// Непрочитанное: ближайшее ниже открытого (открытый сам становится прочитанным).
		m.selectChannel(order[1])
		want := ""
		for step := 1; step < len(order) && want == ""; step++ {
			if id := order[(1+step)%len(order)]; m.rowUnread(id) {
				want = id
			}
		}
		if want == "" {
			t.Error("nothing unread to step to")
			return
		}
		press("Alt+Shift+Down")
		if m.current != want {
			t.Errorf("Alt+Shift+Down: %s, want %s", m.current, want)
		}
		press("Alt+Shift+Up") // назад — к ближайшему непрочитанному выше, не к прочитанному order[1]
		if m.current == order[1] {
			t.Error("Alt+Shift+Up stepped onto a read channel")
		}

		// Alt+↑ в поле ввода — сочетание окна, а не поля.
		m.composer.Focus()
		e := qt.NewQKeyEvent(qt.QEvent__ShortcutOverride, int(qt.Key_Up), qt.AltModifier)
		qt.QCoreApplication_SendEvent(m.composer.edit.QObject, e.QEvent)
		if e.IsAccepted() {
			t.Error("composer overrides Alt+Up")
		}
	})
}
