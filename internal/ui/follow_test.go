package ui

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// Уведомления канала из меню сайдбара: уровень, ответы в тредах, @channel —
// на сервер и в галочки меню.
func TestChannelNotifyMenu(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		backend := channelByName(m, "dev", "backend").Id
		sub := func() []menuItem {
			for _, it := range m.sidebarItems("", backend) {
				if it.title == "Уведомления на рабочий стол" {
					return it.sub
				}
			}
			return nil
		}
		find := func(title string) menuItem {
			for _, it := range sub() {
				if it.title == title {
					return it
				}
			}
			t.Errorf("no %q in %v", title, sub()) // не Fatal: внутри onMain он вешает тесты
			return menuItem{do: func() {}}
		}
		server := func(key string) string {
			mem, _, err := m.s.Client.GetChannelMember(context.Background(), backend, m.s.Me.Id, "")
			if err != nil {
				return "error: " + err.Error()
			}
			return mem.NotifyProps[key]
		}
		if find("Как в профиле (все)").check != checked {
			t.Error("default level is not checked")
		}
		find("Только упоминания").do()
		waitFor(t, "level on the server", func() bool {
			return server(model.DesktopNotifyProp) == model.ChannelNotifyMention && find("Только упоминания").check == checked
		})
		if find("Ответы в тредах, где я участвую").check != checked {
			t.Error("thread replies are not on by default")
		}
		find("Ответы в тредах, где я участвую").do()
		waitFor(t, "thread replies off", func() bool { return server(model.DesktopThreadsNotifyProp) == model.UserNotifyMention })
		find("Игнорировать @channel, @here, @all").do()
		waitFor(t, "ignore channel mentions", func() bool {
			return server(model.IgnoreChannelMentionsNotifyProp) == model.IgnoreChannelMentionsOn &&
				find("Игнорировать @channel, @here, @all").check == checked
		})
	})
}

// Подписка на тред колокольчиком в шапке панели: отписались — тред пропал
// из «Тредов», подписались — вернулся.
func TestThreadFollow(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		m.openThread(root.Id)
		if !waitFor(t, "thread with follow button", func() bool { return m.thread != nil && m.thread.channelID != "" && m.panel.follow.IsVisible() }) {
			return
		}
		was, _ := m.threadFollowing(root.Id)
		m.panel.follow.Click()
		if !waitFor(t, "follow toggled", func() bool { f, _ := m.threadFollowing(root.Id); return f != was }) {
			return
		}
		m.panel.follow.Click()
		waitFor(t, "follow toggled back", func() bool { f, _ := m.threadFollowing(root.Id); return f == was })
	})
}
