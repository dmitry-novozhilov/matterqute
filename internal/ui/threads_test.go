package ui

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/markdown"
	"matterqute/internal/mm"
	"matterqute/internal/mmfake"
	"matterqute/internal/store"
)

// startFake поднимает mmfake в нужном режиме CRT и входит как alice.
func startFake(t *testing.T, crt string) (*mmfake.Server, *mm.Session) {
	fake := mmfake.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	fake.CollapsedThreads = crt
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	s, err := mm.Login(context.Background(), srv.URL, "alice", mmfake.Password, "")
	if err != nil {
		t.Fatalf("login to fake: %v", err)
	}
	return fake, s
}

// openTown открывает окно и dev/town-square; возвращает корень засеянного треда.
func openTown(t *testing.T, m *MainWindow) *model.Post {
	m.Show()
	if !waitFor(t, "sidebar and websocket", func() bool { return len(m.items) > 0 && m.connectedOnce }) {
		return nil
	}
	town := channelByName(m, "dev", "town-square")
	m.sidebar.SetCurrentItem(m.items[town.Id])
	if !waitFor(t, "town-square posts", func() bool { return len(m.feed.items) == pageSize }) {
		return nil
	}
	for _, it := range m.feed.items {
		if it.post.ReplyCount > 0 {
			return it.post
		}
	}
	t.Error("no thread root in town-square feed")
	return nil
}

// shot сохраняет окно в $MATTERQUTE_TEST_SHOTS/<name>.png — глазами посмотреть,
// как выглядит то, что проверил тест.
func shot(t *testing.T, m *MainWindow, name string) {
	dir := os.Getenv("MATTERQUTE_TEST_SHOTS")
	if dir == "" {
		return
	}
	m.w.Resize(1400, 800)
	settleFor(300 * time.Millisecond)
	path := filepath.Join(dir, name+".png")
	if !m.w.Grab().Save(path) {
		t.Errorf("save screenshot %s", path)
	}
}

func feedPost(f *Feed, id string) *model.Post {
	if i := f.index(id); i >= 0 {
		return f.items[i].post
	}
	return nil
}

func reactionUsers(p *model.Post, name string) int {
	for _, g := range groupReactions(p) {
		if g.name == name {
			return len(g.users)
		}
	}
	return 0
}

func TestThreadsCRT(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() { testThreadsCRT(t, fake, s) })
}

func testThreadsCRT(t *testing.T, fake *mmfake.Server, s *mm.Session) {
	m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
	defer m.Close()
	root := openTown(t, m)
	if root == nil {
		return
	}
	if !m.st.CRT() {
		t.Error("CRT not detected with CollapsedThreads=always_on")
		return
	}
	// Засеяно: в треде упомянули alice, она этого не видела.
	if unread, mentions := m.st.ThreadsUnread(); !unread || mentions != 1 || m.items[threadsID] == nil {
		t.Errorf("threads: unread=%v mentions=%d item=%v; want true 1 present", unread, mentions, m.items[threadsID] != nil)
	}
	for _, it := range m.feed.items {
		if it.post.RootId != "" {
			t.Errorf("CRT channel feed shows reply %s", it.post.Id)
			break
		}
	}
	if root.ReplyCount != 3 || reactionUsers(root, "+1") != 2 || reactionUsers(root, "tada") != 1 {
		t.Errorf("root: %d replies, +1×%d, tada×%d; want 3, 2, 1", root.ReplyCount, reactionUsers(root, "+1"), reactionUsers(root, "tada"))
	}

	// Открыли тред — он прочитан.
	m.openThread(root.Id)
	if !waitFor(t, "thread loaded", func() bool { return len(m.panel.feed.items) == 4 }) {
		return
	}
	if unread, mentions := m.st.ThreadsUnread(); unread || mentions != 0 {
		t.Errorf("after opening thread: unread=%v mentions=%d", unread, mentions)
	}

	// Свой ответ: в треде один раз, в канале — только счётчик у корня.
	m.sendReply("ответ из теста", nil, "")
	waitFor(t, "own reply in thread", func() bool { return len(m.panel.feed.items) == 5 })
	waitFor(t, "reply count 4", func() bool { p := feedPost(m.feed, root.Id); return p != nil && p.ReplyCount == 4 })
	settleFor(200 * time.Millisecond)
	if n := len(m.panel.feed.items); n != 5 {
		t.Errorf("thread has %d posts after own reply, want 5", n)
	}
	if n := len(m.feed.items); n != pageSize {
		t.Errorf("channel feed changed by reply: %d items", n)
	}

	// Чужой ответ по WebSocket.
	fake.Post(&model.Post{ChannelId: root.ChannelId, RootId: root.Id, UserId: fake.UserID("bob"), Message: "и от bob"})
	waitFor(t, "bob reply in thread", func() bool { return len(m.panel.feed.items) == 6 })
	waitFor(t, "reply count 5", func() bool { p := feedPost(m.feed, root.Id); return p != nil && p.ReplyCount == 5 })

	// Реакции: снять свою, получить чужую — и в канале, и в треде.
	m.toggleReaction(feedPost(m.feed, root.Id), "+1")
	waitFor(t, "own +1 removed", func() bool { return reactionUsers(feedPost(m.feed, root.Id), "+1") == 1 })
	fake.React(fake.UserID("bob"), root.Id, "fire", true)
	waitFor(t, "bob fire in channel", func() bool { return reactionUsers(feedPost(m.feed, root.Id), "fire") == 1 })
	waitFor(t, "bob fire in thread", func() bool { return reactionUsers(feedPost(m.panel.feed, root.Id), "fire") == 1 })
	if n := reactionUsers(feedPost(m.panel.feed, root.Id), "+1"); n != 1 {
		t.Errorf("thread root +1×%d, want 1", n)
	}
	shot(t, m, "crt-thread")

	// Список тредов.
	m.sidebar.SetCurrentItem(m.items[threadsID])
	if m.center.CurrentWidget().UnsafePointer() != m.threadsPage.UnsafePointer() || m.threadsList.Count() != 1 {
		t.Errorf("threads view: page switched=%v, %d items; want true, 1",
			m.center.CurrentWidget().UnsafePointer() == m.threadsPage.UnsafePointer(), m.threadsList.Count())
	}
	shot(t, m, "crt-threads-list")
}

func TestThreadsWithoutCRT(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsDisabled)
	onMain(func() { testThreadsWithoutCRT(t, fake, s) })
}

func testThreadsWithoutCRT(t *testing.T, fake *mmfake.Server, s *mm.Session) {
	m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
	defer m.Close()
	root := openTown(t, m)
	if root == nil {
		return
	}
	if m.st.CRT() || m.items[threadsID] != nil {
		t.Error("CRT on or threads item shown with CollapsedThreads=disabled")
	}
	replies := 0
	for _, it := range m.feed.items {
		if it.post.RootId == root.Id {
			replies++
		}
	}
	if replies != 3 {
		t.Errorf("channel feed shows %d replies inline, want 3", replies)
	}

	m.openThread(root.Id)
	if !waitFor(t, "thread loaded", func() bool { return len(m.panel.feed.items) == 4 }) {
		return
	}
	// Ответ виден и в канале, и в треде, по одному разу.
	fake.Post(&model.Post{ChannelId: root.ChannelId, RootId: root.Id, UserId: fake.UserID("bob"), Message: "ответ без CRT"})
	waitFor(t, "reply in thread", func() bool { return len(m.panel.feed.items) == 5 })
	waitFor(t, "reply in channel", func() bool { return len(m.feed.items) == pageSize+1 })
	waitFor(t, "reply count 4", func() bool { p := feedPost(m.feed, root.Id); return p != nil && p.ReplyCount == 4 })
	shot(t, m, "nocrt-thread")
}

func TestPostsOfThreadOrder(t *testing.T) {
	// Тред приходит как PostList; в панели посты должны идти от корня.
	list := &model.PostList{Order: []string{"r2", "r1", "root"}, Posts: map[string]*model.Post{
		"root": {Id: "root", CreateAt: 1}, "r1": {Id: "r1", RootId: "root", CreateAt: 2}, "r2": {Id: "r2", RootId: "root", CreateAt: 3},
	}}
	if ps := store.PostsOf(list); ps[0].Id != "root" || ps[2].Id != "r2" {
		t.Errorf("thread order: %s..%s", ps[0].Id, ps[2].Id)
	}
}

// В списке тредов первая строка корня — с оформлением, без markdown-символов.
func TestThreadPreviewMarkdown(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		m.selectChannel(threadsID)
		th := m.st.Thread(root.Id)
		if !waitFor(t, "threads list", func() bool { return m.threadsList.Count() == 1 }) || th == nil || th.Post == nil {
			return
		}
		// Засеянный корень — простым текстом; подменяем на разметку.
		th.Post.Message = "**Релиз** `v2.1` готов :+1: — [заметки](https://example.com/notes) и очень длинный хвост, который в строку списка тредов никак не влезет"
		got := markdown.PreviewLine(th.Post.Message, markdown.Options{Palette: Pal()})
		if !strings.Contains(got, "<strong>Релиз</strong>") || strings.Contains(got, "**") || !strings.Contains(got, "👍") {
			t.Errorf("thread preview %q", got)
		}
		m.threadsList.Viewport().Update()
		shot(t, m, "threads-markdown")

		// Не влезает — обрезано с «…», оформление в начале цело.
		doc := richLineDoc(got, 160, pixelFont(14, qt.QFont__Normal))
		defer doc.Delete()
		if text := doc.ToPlainText(); doc.IdealWidth() > 160 || !strings.HasPrefix(text, "Релиз") || !strings.HasSuffix(text, "…") {
			t.Errorf("elided preview %q, %.0f px wide", text, doc.IdealWidth())
		}
		if html := doc.ToHtml(); !strings.Contains(html, "font-weight") {
			t.Error("elided preview lost the bold")
		}
	})
}
