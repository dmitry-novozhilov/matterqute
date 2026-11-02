package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// Ctrl+K: пусто — недавние (без открытого), свой канал, личка, человек без
// лички (личка создаётся), открытый канал, где нас нет (вход).
func TestSwitcher(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		town := m.current
		for _, it := range rankSwitch(m.switchSources(nil, nil), "", town) {
			if it.id == town {
				t.Error("recent list offers the open channel")
			}
		}
		run := func(query string, wait func() bool, label string) bool {
			m.showSwitcher()
			d := m.switcherDialog
			var field *qt.QLineEdit
			var list *qt.QListWidget
			for _, o := range descendants(d.w.QObject) {
				switch {
				case o.Inherits("QLineEdit") && field == nil:
					field = qt.UnsafeNewQLineEdit(o.UnsafePointer())
				case o.Inherits("QListWidget"):
					list = qt.UnsafeNewQListWidget(o.UnsafePointer())
				}
			}
			field.SetText(query)
			if !waitFor(t, label+": first row", func() bool {
				return list.Count() > 0 && strings.HasPrefix(list.Item(0).Text(), strings.SplitN(query, " ", 2)[0])
			}) {
				return false
			}
			if label == "join" {
				// Канал, где нас нет, знает только сервер: ждём его ответа.
				if !waitFor(t, "join row from the server", func() bool { return strings.Contains(list.Item(0).Text(), "войти") }) {
					return false
				}
			}
			qt.QCoreApplication_SendEvent(field.QObject, qt.NewQKeyEvent(qt.QEvent__KeyPress, int(qt.Key_Return), qt.NoModifier).QEvent)
			return waitFor(t, label, wait)
		}
		backend := channelByName(m, "dev", "backend")
		if !run("back", func() bool { return m.current == backend.Id }, "own channel") {
			return
		}
		bob := m.st.UserByName("bob")
		isDMWith := func(u *model.User) bool {
			ch := m.st.Channel(m.current)
			return ch != nil && ch.Type == model.ChannelTypeDirect && m.st.DMPeer(ch) == u.Id
		}
		if !run("bob", func() bool { return isDMWith(bob) }, "direct message") {
			return
		}
		dave := m.st.UserByName("dave")
		if !run("dave", func() bool { return isDMWith(dave) }, "new direct message") {
			return
		}
		run("frontend", func() bool { ch := m.st.Channel(m.current); return ch != nil && ch.Name == "frontend" }, "join")
	})
}
