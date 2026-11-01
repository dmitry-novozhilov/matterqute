package ui

import (
	"io"
	"log/slog"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// Ленты всех каналов и личек грузятся заранее: канал открывается сразу с
// постами, новые посты в нём копятся по WebSocket; после переподключения — заново.
func TestPreload(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		m.Show()
		all := func() bool {
			if len(m.items) == 0 {
				return false
			}
			for id := range m.items {
				if id == threadsID {
					continue
				}
				if tl := m.st.Timeline(id); tl == nil || !tl.Loaded {
					return false
				}
			}
			return true
		}
		if !waitFor(t, "every channel preloaded", all) {
			return
		}
		if m.preloading != 0 || len(m.preloadQueue) != 0 {
			t.Errorf("preload not finished: %d running, %d queued", m.preloading, len(m.preloadQueue))
		}

		// Пост в незакрытом канале ложится в его заранее загруженную ленту.
		backend := channelByName(m, "dev", "backend")
		if m.current == backend.Id {
			t.Fatal("backend is open: test is meaningless")
		}
		bob := m.st.UserByName("bob")
		p := fake.Post(&model.Post{ChannelId: backend.Id, UserId: bob.Id, Message: "пока канал закрыт"})
		waitFor(t, "post in the preloaded timeline", func() bool { return m.st.Timeline(backend.Id).Get(p.Id) != nil })

		// Открыли — посты сразу, без «Загружаем сообщения…».
		m.selectChannel(backend.Id)
		if m.feed.placeholder || !m.feed.Has(p.Id) {
			t.Errorf("backend opened without its posts at once: placeholder=%v, new post shown=%v", m.feed.placeholder, m.feed.Has(p.Id))
		}

		// Переподключение: ленты сброшены и грузятся заново, все — и закрытые тоже.
		alerts := channelByName(m, "ops", "alerts")
		old := m.st.Timeline(alerts.Id)
		m.OnConnected()
		if !waitFor(t, "preloaded again after reconnect", func() bool {
			tl := m.st.Timeline(alerts.Id)
			return tl != nil && tl != old && all()
		}) {
			return
		}
	})
}
