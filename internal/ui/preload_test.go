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

		// Лента закрытого канала готова заранее: нарисована фоном, с новым постом.
		f := m.feeds[backend.Id]
		if !waitFor(t, "backend feed drawn in background", func() bool { return f != nil && f.ready && !f.hasWork() && f.index(p.Id) >= 0 }) {
			return
		}
		first := f.items[0].frame

		// Открыли — та же лента, без перерисовки и «Загружаем сообщения…».
		m.selectChannel(backend.Id)
		if m.feed != f || f.placeholder || f.items[0].frame != first {
			t.Errorf("backend opened not from its ready feed: same=%v placeholder=%v", m.feed == f, f.placeholder)
		}

		// Обрыв связи: о посте в закрытом канале событие не пришло. После
		// переподключения ленты сверяются с сервером (не грузятся заново) — пост
		// и в store, и в скрытой ленте.
		alerts := channelByName(m, "ops", "alerts")
		old := m.st.Timeline(alerts.Id)
		missed := fake.PostMissed(&model.Post{ChannelId: alerts.Id, UserId: bob.Id, Message: "пока не было связи"})
		m.OnConnected()
		if !waitFor(t, "missed post synced into the hidden feed", func() bool {
			af := m.feeds[alerts.Id]
			return af != nil && af.Has(missed.Id) && old.Get(missed.Id) != nil
		}) {
			return
		}
		if m.st.Timeline(alerts.Id) != old || old.Stale {
			t.Errorf("timeline reloaded instead of synced (same %v, stale %v)", m.st.Timeline(alerts.Id) == old, old.Stale)
		}
	})
}
