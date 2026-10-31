package ui

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
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
	m.send("привет из теста", nil)
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
	if waitFor(t, "mention in backend", func() bool { _, n := m.st.Unread(backend.Id); return n == 1 }) {
		if !strings.Contains(m.items[backend.Id].Text(0), "(1)") {
			t.Errorf("sidebar item %q lacks mention counter", m.items[backend.Id].Text(0))
		}
	}
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
