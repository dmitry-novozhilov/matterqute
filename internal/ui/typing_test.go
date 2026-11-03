package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/mmfake"
)

func TestTyping(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		defer SetTyping(true, true)
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		town := m.current
		bob, carol, dave := fake.UserID("bob"), fake.UserID("carol"), fake.UserID("dave")
		note := func(h *header) string { return h.note }

		// Чужой набор — в шапке канала; сообщение от пишущего убирает его.
		fake.Typing(bob, town, "")
		if !waitFor(t, "bob typing", func() bool { return note(m.head) == "bob печатает…" }) {
			return
		}
		fake.Typing(carol, town, "")
		waitFor(t, "two typing", func() bool { return note(m.head) == "bob и carol печатают…" })
		if !m.head.sub.IsVisibleTo(m.head.f.QWidget) || !strings.Contains(m.head.sub.Text(), "bob и carol печатают…") {
			t.Errorf("header line %q", m.head.sub.Text())
		}
		shotWidget(t, m.w, "typing")
		fake.Post(&model.Post{ChannelId: town, UserId: bob, Message: "готово"})
		waitFor(t, "bob posted", func() bool { return note(m.head) == "carol печатает…" })
		m.expireTyping(time.Now().Add(typingFor + time.Second))
		if note(m.head) != "" || m.head.sub.Text() != m.head.subHTML {
			t.Errorf("typing not expired: %q, header line %q", note(m.head), m.head.sub.Text())
		}

		// В треде — в шапке треда, не канала.
		m.openThread(root.Id)
		if !waitFor(t, "thread", func() bool { return len(m.panel.feed.items) == 4 }) {
			return
		}
		fake.Typing(dave, town, root.Id)
		waitFor(t, "dave typing in the thread", func() bool { return note(m.panel.head) == "dave печатает…" && note(m.head) == "" })

		// Свой набор — серверу, не чаще раза в typingEvery.
		key := func(c *Composer) {
			qt.QCoreApplication_SendEvent(c.edit.QObject, qt.NewQKeyEvent3(qt.QEvent__KeyPress, int(qt.Key_unknown), qt.NoModifier, "а").QEvent)
		}
		sent := func(ev mmfake.TypingEvent) int {
			n := 0
			for _, e := range fake.Typed() {
				if e == ev {
					n++
				}
			}
			return n
		}
		me := m.s.Me.Id
		key(m.composer)
		key(m.composer)
		if !waitFor(t, "own typing sent", func() bool { return sent(mmfake.TypingEvent{UserID: me, ChannelID: town}) == 1 }) {
			return
		}
		key(m.panel.composer)
		waitFor(t, "own typing in the thread", func() bool { return sent(mmfake.TypingEvent{UserID: me, ChannelID: town, ParentID: root.Id}) == 1 })
		settleFor(200 * time.Millisecond)
		if n := sent(mmfake.TypingEvent{UserID: me, ChannelID: town}); n != 1 {
			t.Errorf("typing sent %d times for two keys", n)
		}

		// Не показывать: в шапке своя строка, события не копятся.
		SetTyping(false, true)
		if m.head.note != "" || m.head.sub.Text() != m.head.subHTML {
			t.Errorf("typing note %q with «show» off", m.head.note)
		}
		fake.Typing(bob, town, "")
		settleFor(200 * time.Millisecond)
		if note(m.head) != "" || len(m.typing.who) != 0 {
			t.Errorf("typing shown with «show» off: %q", note(m.head))
		}
		// Не сообщать: набор не уходит.
		SetTyping(true, false)
		m.typing.sent = map[typingKey]time.Time{}
		key(m.composer)
		settleFor(200 * time.Millisecond)
		if n := sent(mmfake.TypingEvent{UserID: me, ChannelID: town}); n != 1 {
			t.Errorf("typing sent with «send» off: %d", n)
		}
	})
}

func TestTypingMenu(t *testing.T) {
	onMain(func() {
		defer SetTyping(true, true)
		var got [][2]bool
		OnTypingChosen = func(show, send bool) { got = append(got, [2]bool{show, send}) }
		defer func() { OnTypingChosen = nil }()
		s := NewShell()
		defer s.w.Close()
		show, send := s.typingActs[0], s.typingActs[1]
		if !show.IsChecked() || !send.IsChecked() || show.Text() != "Показывать, кто печатает" {
			t.Errorf("menu: %q %v, %q %v", show.Text(), show.IsChecked(), send.Text(), send.IsChecked())
		}
		send.Trigger()
		show.Trigger()
		if len(got) != 2 || got[0] != [2]bool{true, false} || got[1] != [2]bool{false, false} || style.typingShow || style.typingSend {
			t.Errorf("chosen %v, style %v %v", got, style.typingShow, style.typingSend)
		}
	})
}

func TestTaskbarAlertMenu(t *testing.T) {
	onMain(func() {
		defer SetTaskbarAlert(true)
		var got []bool
		OnTaskbarAlertChosen = func(on bool) { got = append(got, on) }
		defer func() { OnTaskbarAlertChosen = nil }()
		s := NewShell()
		defer s.w.Close()
		if !s.alertAct.IsChecked() {
			t.Error("alert unchecked by default")
		}
		s.alertAct.Trigger()
		if len(got) != 1 || got[0] || style.alert {
			t.Errorf("chosen %v, style %v", got, style.alert)
		}
	})
}
