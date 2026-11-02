package ui

import (
	"io"
	"log/slog"
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
		note := func(c *Composer) string { return c.typingNote.Text() }

		// Чужой набор — под полем канала; сообщение от пишущего убирает его.
		fake.Typing(bob, town, "")
		if !waitFor(t, "bob typing", func() bool { return note(m.composer) == "bob печатает…" }) {
			return
		}
		fake.Typing(carol, town, "")
		waitFor(t, "two typing", func() bool { return note(m.composer) == "bob и carol печатают…" })
		shotWidget(t, m.w, "typing")
		fake.Post(&model.Post{ChannelId: town, UserId: bob, Message: "готово"})
		waitFor(t, "bob posted", func() bool { return note(m.composer) == "carol печатает…" })
		m.expireTyping(time.Now().Add(typingFor + time.Second))
		if note(m.composer) != "" {
			t.Errorf("typing not expired: %q", note(m.composer))
		}

		// В треде — под полем треда, не канала.
		m.openThread(root.Id)
		if !waitFor(t, "thread", func() bool { return len(m.panel.feed.items) == 4 }) {
			return
		}
		fake.Typing(dave, town, root.Id)
		waitFor(t, "dave typing in the thread", func() bool { return note(m.panel.composer) == "dave печатает…" && note(m.composer) == "" })

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

		// Не показывать: строки нет, события не копятся.
		SetTyping(false, true)
		if m.composer.typingNote.IsVisibleTo(m.composer.w) {
			t.Error("typing line shown with «show» off")
		}
		fake.Typing(bob, town, "")
		settleFor(200 * time.Millisecond)
		if note(m.composer) != "" || len(m.typing.who) != 0 {
			t.Errorf("typing shown with «show» off: %q", note(m.composer))
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
