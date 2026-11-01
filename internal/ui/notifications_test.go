package ui

import (
	"io"
	"log/slog"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/notify"
)

type fakeNotifier struct{ shown []notify.Notification }

func (f *fakeNotifier) Show(n notify.Notification) (uint32, error) {
	f.shown = append(f.shown, n)
	return uint32(len(f.shown)), nil
}

func TestNotifications(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		notes := &fakeNotifier{}
		m.notifier = notes
		raised := 0
		m.OnRaise = func() { raised++ }
		root := openTown(t, m)
		if root == nil {
			return
		}
		bob := m.st.UserByName("bob")
		backend := channelByName(m, "dev", "backend")
		last := func() notify.Notification { return notes.shown[len(notes.shown)-1] }
		post := func(p *model.Post, want int) bool {
			fake.Post(p)
			return waitFor(t, p.Message, func() bool { return len(notes.shown) == want })
		}

		// Пост в другом канале: «bob в #backend», текст без разметки.
		if !post(&model.Post{ChannelId: backend.Id, UserId: bob.Id, Message: "**Деплой** готов :+1:"}, 1) {
			return
		}
		if n := last(); n.Title != "bob в #backend" || n.Body != "Деплой готов 👍" || n.Replaces != 0 {
			t.Errorf("first notification %+v", n)
		}
		// Следующий пост там же заменяет уведомление, а не копит их.
		post(&model.Post{ChannelId: backend.Id, UserId: bob.Id, Message: "и ещё"}, 2)
		if n := last(); n.Replaces != 1 {
			t.Errorf("second post in backend: Replaces = %d, want 1", n.Replaces)
		}

		// Ответ в треде, на который alice подписана: канал открыт, но тред — нет.
		if !post(&model.Post{ChannelId: root.ChannelId, RootId: root.Id, UserId: bob.Id, Message: "в треде"}, 3) {
			return
		}
		if n := last(); n.Title != "bob в треде, #Town Square" {
			t.Errorf("thread reply title %q", n.Title)
		}

		// Свой пост — без уведомления.
		fake.Post(&model.Post{ChannelId: backend.Id, UserId: m.s.Me.Id, Message: "мой"})
		settleFor(200e6)
		if len(notes.shown) != 3 {
			t.Errorf("own post notified: %+v", last())
		}

		// Окно активно и канал открыт — молчим; неактивно — уведомляем.
		m.w.ActivateWindow()
		settle()
		town := m.current
		if m.w.IsActiveWindow() {
			fake.Post(&model.Post{ChannelId: town, UserId: bob.Id, Message: "в открытом канале"})
			settleFor(200e6)
			if len(notes.shown) != 3 {
				t.Errorf("post in the open channel of the active window notified: %+v", last())
			}
		} else {
			t.Log("offscreen did not activate the window: open-channel check skipped")
		}

		// Клик по уведомлению: окно наверх, канал поста.
		notes.shown[1].OnOpen("")
		waitFor(t, "backend opened from notification", func() bool { return m.current == backend.Id })
		if raised != 1 {
			t.Errorf("window raised %d times, want 1", raised)
		}
	})
}

func TestNotificationBody(t *testing.T) {
	p := &model.Post{Message: ""}
	p.AddProp(model.PostPropsAttachments, []*model.MessageAttachment{{Title: "[FIRING:1] DiskSpaceLow", Text: "Свободно **3%**"}})
	if got := notificationBody(p); got != "[FIRING:1] DiskSpaceLow" {
		t.Errorf("attachment body %q", got)
	}
	files := &model.Post{Metadata: &model.PostMetadata{Files: []*model.FileInfo{{Name: "a.pdf"}, {Name: "b.png"}, {Name: "c.txt"}, {Name: "d.zip"}}}}
	if got := notificationBody(files); got != "📎 a.pdf, b.png и ещё 2" {
		t.Errorf("files body %q", got)
	}
}
