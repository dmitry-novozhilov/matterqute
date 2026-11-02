package ui

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/markdown"
	"matterqute/internal/sched"
)

// Сервер без отложенных (без лицензии): хранит и отправляет приложение.
func TestScheduledLocal(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	fake.NoScheduledPosts = true
	cacheDir := t.TempDir()
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", cacheDir)
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		if !waitFor(t, "server checked", func() bool { return m.sched.probed && !m.sched.server }) {
			return
		}
		town := m.current
		c := m.composer
		last := func() *model.Post { return m.feed.items[len(m.feed.items)-1].post }

		// Ctrl+Shift+Enter — окно «Отправить позже»; «Через час» — отложено.
		c.setBlocks(markdown.ToBlocks("Не забудьте **отчёт**"))
		qt.QCoreApplication_SendEvent(c.edit.QObject, qt.NewQKeyEvent(qt.QEvent__KeyPress, int(qt.Key_Return), qt.ControlModifier|qt.ShiftModifier).QEvent)
		d := m.schedDialog
		if d == nil || d.closed || !d.w.IsVisible() {
			t.Error("Ctrl+Shift+Enter did not open «send later»")
			return
		}
		shotWidget(t, d.w.QWidget, "schedule-dialog")
		var hour *qt.QPushButton
		for _, o := range descendants(d.w.QObject) {
			if o.Inherits("QPushButton") && strings.HasPrefix(qt.UnsafeNewQPushButton(o.UnsafePointer()).Text(), "Через час") {
				hour = qt.UnsafeNewQPushButton(o.UnsafePointer())
			}
		}
		if hour == nil {
			t.Error("no «Через час» preset")
			return
		}
		hour.Click()
		if !c.empty() || len(m.sched.posts) != 1 {
			t.Errorf("after scheduling: composer empty %v, scheduled %d", c.empty(), len(m.sched.posts))
			return
		}
		p := m.sched.posts[0]
		if p.Message != "Не забудьте **отчёт**" || p.ChannelID != town || p.OnServer || time.Until(p.Time()) < 55*time.Minute {
			t.Errorf("scheduled %+v", p)
		}
		if m.items[schedID] == nil || !c.schedNote.IsVisibleTo(c.w) || !strings.Contains(c.schedNote.Text(), "1 отложенное сообщение") {
			t.Errorf("sidebar item %v, note %q", m.items[schedID] != nil, c.schedNote.Text())
		}
		saved, err := sched.Load(filepath.Join(cacheDir, "scheduled.json"))
		if err != nil || len(saved) != 1 || saved[0].ID != p.ID {
			t.Errorf("saved %+v %v", saved, err)
		}

		// Список: строка с каналом, временем, текстом.
		m.selectChannel(schedID)
		if m.current != schedID || m.center.CurrentWidget().UnsafePointer() != m.schedPage.UnsafePointer() || labelText(m.schedPage.QObject, "Town Square") == nil {
			t.Error("scheduled page not shown")
		}
		shot(t, m, "scheduled-page")

		// «Изменить» — текст в поле канала, Enter сохраняет.
		m.editScheduled(p.ID)
		if c.Editing() != p.ID || !strings.HasPrefix(c.editText.Text(), "Отложенное (") {
			t.Errorf("edit scheduled: editing %q, %q", c.Editing(), c.editText.Text())
		}
		c.setBlocks(markdown.ToBlocks("Не забудьте **отчёт** до пятницы"))
		qt.QCoreApplication_SendEvent(c.edit.QObject, qt.NewQKeyEvent(qt.QEvent__KeyPress, int(qt.Key_Return), qt.NoModifier).QEvent)
		if p := m.sched.find(p.ID); p == nil || p.Message != "Не забудьте **отчёт** до пятницы" {
			t.Errorf("edited scheduled %+v", p)
		}

		// Время пришло (приложение работает) — уходит само.
		m.sched.update(p.ID, func(p *sched.Post) { p.At = time.Now().Add(2 * time.Minute).UnixMilli() })
		m.sched.tick(time.Now().Add(3 * time.Minute))
		m.selectChannel(town)
		if !waitFor(t, "sent on time", func() bool {
			return len(m.sched.posts) == 0 && last().Message == "Не забудьте **отчёт** до пятницы"
		}) {
			return
		}
		if m.items[schedID] != nil || c.schedNote.IsVisibleTo(c.w) {
			t.Error("«scheduled» still shown with nothing scheduled")
		}
		if _, err := os.Stat(filepath.Join(cacheDir, "scheduled.json")); !os.IsNotExist(err) {
			t.Errorf("scheduled file left: %v", err)
		}

		// Приложение было закрыто: давно пропущенное не уходит само.
		missed := &sched.Post{ID: "local-missed", ChannelID: town, Message: "старое", At: time.Now().Add(-time.Hour).UnixMilli()}
		if err := sched.Save(filepath.Join(cacheDir, "scheduled.json"), []*sched.Post{missed}); err != nil {
			t.Error(err)
			return
		}
		m2 := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", cacheDir)
		defer m2.Close()
		m2.Show()
		if !waitFor(t, "second window loads the file", func() bool { return m2.sched.probed && m2.sched.find("local-missed") != nil }) {
			return
		}
		m2.sched.tick(time.Now())
		settleFor(300 * time.Millisecond)
		if m2.sched.find("local-missed") == nil {
			t.Error("missed post was sent by itself")
		}
		m2.selectChannel(schedID)
		if labelText(m2.schedPage.QObject, "не ушло вовремя") == nil {
			t.Error("missed post is not marked")
		}
		m2.sched.sendNow("local-missed")
		waitFor(t, "missed post sent on request", func() bool { return m2.sched.find("local-missed") == nil })
	})
}

// Сервер с отложенными: хранит и отправляет он, приложение — по событиям.
func TestScheduledServer(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		if !waitFor(t, "server mode", func() bool { return m.sched.probed && m.sched.server }) {
			return
		}
		c := m.composer
		c.setBlocks(markdown.ToBlocks("с сервера"))
		c.ScheduleAt(time.Now().Add(time.Hour))
		if !waitFor(t, "scheduled on the server", func() bool {
			return fake.Scheduled() == 1 && len(m.sched.posts) == 1 && m.sched.posts[0].OnServer
		}) {
			return
		}
		id := m.sched.posts[0].ID
		settleFor(200 * time.Millisecond) // событие created — не задваивает
		if len(m.sched.posts) != 1 {
			t.Errorf("%d scheduled after the event", len(m.sched.posts))
		}

		m.sched.update(id, func(p *sched.Post) { p.Message = "с сервера, исправлено" })
		waitFor(t, "updated on the server", func() bool {
			p := m.sched.find(id)
			return p != nil && p.Message == "с сервера, исправлено"
		})

		// Сервер отправил в срок: отложенное пропало, сообщение в ленте.
		fake.ProcessScheduled(time.Now().Add(2 * time.Hour))
		waitFor(t, "sent by the server", func() bool {
			return len(m.sched.posts) == 0 && m.feed.items[len(m.feed.items)-1].post.Message == "с сервера, исправлено"
		})

		// Удалить.
		c.setBlocks(markdown.ToBlocks("лишнее"))
		c.ScheduleAt(time.Now().Add(time.Hour))
		if !waitFor(t, "second scheduled", func() bool { return len(m.sched.posts) == 1 }) {
			return
		}
		m.sched.remove(m.sched.posts[0].ID, nil)
		waitFor(t, "removed on the server", func() bool { return fake.Scheduled() == 0 && len(m.sched.posts) == 0 })
	})
}
