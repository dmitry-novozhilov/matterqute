package ui

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/mm"
	"matterqute/internal/mmfake"
)

// waitFor крутит цикл событий, пока cond не станет true (сеть отвечает из горутин).
func waitFor(t *testing.T, what string, cond func() bool) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		qt.QCoreApplication_ProcessEvents()
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("timeout waiting for %s", what)
	return false
}

func channelByName(m *MainWindow, teamName, name string) *model.Channel {
	for _, team := range m.st.Teams() {
		if team.Name != teamName {
			continue
		}
		for _, ch := range m.st.TeamChannels(team.Id) {
			if ch.Name == name {
				return ch
			}
		}
	}
	return nil
}

func TestMainWindowFeed(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fake := mmfake.New(log)
	srv := httptest.NewServer(fake)
	defer srv.Close()
	s, err := mm.Login(context.Background(), srv.URL, "alice", mmfake.Password, "")
	if err != nil {
		t.Fatalf("login to fake: %v", err)
	}
	onMain(func() { testMainWindowFeed(t, s, fake) })
}

func testMainWindowFeed(t *testing.T, s *mm.Session, fake *mmfake.Server) {
	m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
	m.Show()
	defer m.Close()

	if !waitFor(t, "sidebar and websocket", func() bool { return len(m.items) > 0 && m.connectedOnce }) {
		return
	}
	town, backend := channelByName(m, "dev", "town-square"), channelByName(m, "dev", "backend")
	if town == nil || backend == nil {
		t.Errorf("dev channels not in sidebar: town=%v backend=%v", town, backend)
		return
	}
	if unread, _ := m.st.Unread(backend.Id); !unread {
		t.Error("backend is seeded unread but shows as read")
	}

	m.sidebar.SetCurrentItem(m.items[town.Id])
	if !waitFor(t, "town-square posts", func() bool { return len(m.feed.items) == pageSize }) {
		return
	}

	// Свой пост приходит и ответом на CreatePost, и по WebSocket — в ленте он один.
	m.send("привет из теста", nil, "", nil)
	mine := func() int {
		n := 0
		for _, it := range m.feed.items {
			if it.post.Message == "привет из теста" {
				n++
			}
		}
		return n
	}
	waitFor(t, "own post in feed", func() bool { return mine() == 1 })
	settleFor(300 * time.Millisecond)
	if n := mine(); n != 1 {
		t.Errorf("own post shown %d times, want 1", n)
	}

	// Упоминание в другом канале: жирный и счётчик в сайдбаре, лента не трогается.
	before := len(m.feed.items)
	bob := ""
	for _, id := range m.st.DMPeers() {
		if m.st.Username(id) == "bob" {
			bob = id
		}
	}
	fake.Post(&model.Post{ChannelId: backend.Id, UserId: bob, Message: "@alice посмотри логи"})
	waitFor(t, "mention in backend", func() bool { _, n := m.st.Unread(backend.Id); return n == 1 })
	if len(m.feed.items) != before {
		t.Errorf("post to another channel changed open feed: %d → %d items", before, len(m.feed.items))
	}

	// Открыли канал — прочитан.
	m.sidebar.SetCurrentItem(m.items[backend.Id])
	if unread, n := m.st.Unread(backend.Id); unread || n != 0 {
		t.Errorf("backend after open: unread=%v mentions=%d", unread, n)
	}
}

func settleFor(d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		qt.QCoreApplication_ProcessEvents()
		time.Sleep(10 * time.Millisecond)
	}
}

// Ширины колонок: восстанавливаются при любой ширине окна, перетаскивание
// разделителя сохраняется, ширину закрытой панели треда сплиттер помнит.
func TestColumns(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		m.SetColumns(300, 450)
		var saved []int
		m.OnColumnsChanged = func(sidebar, thread int) { saved = []int{sidebar, thread} }
		m.w.Resize(1500, 800)
		root := openTown(t, m)
		if root == nil {
			return
		}
		sizes := func() []int { settle(); return m.split.Sizes() }
		if got := sizes(); got[0] != 300 {
			t.Errorf("restored sidebar is %d px in a 1500 px window, want 300 (sizes %v)", got[0], got)
		}
		m.openThread(root.Id)
		if got := sizes(); got[0] != 300 || got[2] != 450 {
			t.Errorf("with thread open: sizes %v, want sidebar 300 and thread 450", got)
		}
		m.w.Resize(1200, 800)
		if got := sizes(); got[0] != 300 || got[2] != 450 {
			t.Errorf("window narrowed: sizes %v, want sidebar 300 and thread 450", got)
		}

		// Перетащили оба разделителя — после паузы ширины уходят на сохранение.
		m.split.MoveSplitter(320, 1)
		m.split.MoveSplitter(1200-400-1, 2)
		if saved != nil {
			t.Errorf("columns saved on every pixel of the drag: %v", saved)
		}
		waitFor(t, "columns saved", func() bool { return saved != nil })
		if len(saved) != 2 || saved[0] != 320 || saved[1] != 400 {
			t.Errorf("saved columns %v, want [320 400]", saved)
		}
		m.closeThread()
		m.openThread(root.Id)
		if got := sizes(); got[2] != 400 {
			t.Errorf("reopened thread panel is %d px, want 400", got[2])
		}
		if sb, th := m.Columns(); sb != 320 || th != 400 {
			t.Errorf("Columns() = %d, %d; want 320, 400", sb, th)
		}

		// Мусор в конфиге не ломает раскладку: ширины в пределах дизайна.
		m.SetColumns(5000, 10)
		if sb, th := m.Columns(); sb != sidebarMax || th != threadMin {
			t.Errorf("SetColumns(5000, 10) → %d, %d; want %d, %d", sb, th, sidebarMax, threadMin)
		}
	})
}

func TestShellGeometry(t *testing.T) {
	onMain(func() {
		// Экран платформы offscreen — 800×600, а restoreGeometry вписывает окно в
		// экран: берём окно, которое помещается.
		a := NewShell()
		closed := false
		a.OnClose = func() { closed = true }
		a.w.SetMinimumSize2(100, 100)
		a.w.Resize(600, 400)
		a.w.Move(40, 50)
		a.Show()
		settle()
		g := a.Geometry()
		a.w.Close()
		if !closed {
			t.Error("Shell.OnClose not called on close")
		}

		b := NewShell()
		defer b.w.Close()
		b.w.SetMinimumSize2(100, 100)
		b.RestoreGeometry(g)
		b.Show()
		settle()
		if sz, pos := b.w.Size(), b.w.Pos(); sz.Width() != 600 || sz.Height() != 400 || pos.X() != 40 || pos.Y() != 50 {
			t.Errorf("restored window %dx%d at %d,%d; want 600x400 at 40,50", sz.Width(), sz.Height(), pos.X(), pos.Y())
		}
	})
}
