package ui

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/config"
	"matterqute/internal/mmfake"
)

// Ссылки на свой сервер открываются здесь: сообщение (ответ в треде — в
// панели треда; старое — с догрузкой истории), канал, личка.
func TestPermalinks(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		ctx := context.Background()
		town := root.ChannelId
		thread, _, err := s.Client.GetPostThread(ctx, root.Id, "", false)
		if err != nil {
			t.Error(err)
			return
		}
		var reply string
		for _, id := range thread.Order {
			if id != root.Id {
				reply = id
			}
		}
		link := func(path string) string { return s.ServerURL + path }

		// Ответ в треде из другого канала: канал корня, корень подсвечен, тред
		// открыт и прокручен к ответу, он подсвечен.
		m.selectChannel(channelByName(m, "dev", "backend").Id)
		m.handleLink(link("/dev/pl/" + reply))
		if !waitFor(t, "reply in the thread panel", func() bool {
			return m.current == town && m.thread != nil && m.thread.rootID == root.Id &&
				m.panel.feed.highlight == reply && m.feed.highlight == root.Id
		}) {
			return
		}
		shot(t, m, "permalink-reply")

		// Старое сообщение — за первой страницей ленты: догружается и видно.
		page, _, err := s.Client.GetPostsForChannel(ctx, town, 2, pageSize, "", true, false)
		if err != nil || len(page.Order) == 0 {
			t.Errorf("third page of town-square: %v", err)
			return
		}
		old := page.Order[len(page.Order)/2]
		if m.feed.Has(old) {
			t.Errorf("post %s is already loaded", old)
			return
		}
		m.handleLink(link("/dev/pl/" + old))
		if !waitFor(t, "old post loaded and highlighted", func() bool { return m.feed.Has(old) && m.feed.highlight == old }) {
			return
		}
		if y := m.feed.postTop(m.feed.index(old)); y < 0 || y > m.feed.view.Viewport().Height() {
			t.Errorf("old post at %d px, viewport %d px high", y, m.feed.view.Viewport().Height())
		}

		// Канал и личка.
		m.handleLink(link("/ops/channels/alerts"))
		waitFor(t, "alerts", func() bool { ch := m.st.Channel(m.current); return ch != nil && ch.Name == "alerts" })
		m.handleLink(link("/dev/messages/@carol"))
		waitFor(t, "DM with carol", func() bool {
			ch := m.st.Channel(m.current)
			return ch != nil && ch.Type == model.ChannelTypeDirect && m.st.DMPeer(ch) == m.st.UserByName("carol").Id
		})

		// Путь своего сервера, который приложение не открывает, — не его дело.
		if path, ok := m.ownPath(link("/admin_console")); !ok || m.openServerPath(path) {
			t.Errorf("admin console: own %v", ok)
		}
		if _, ok := m.ownPath("https://example.com/dev/pl/" + reply); ok {
			t.Error("foreign server link taken as own")
		}
	})
}

// Ссылка снаружи (mattermost:// из браузера): сервер на экран, сообщение в нём;
// незнакомый сервер — страница добавления с его адресом.
func TestOpenLinkFromBrowser(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := mmfake.New(log)
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	tokens := config.FileTokens(filepath.Join(dir, "tokens.json"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	host := func(raw string) string { u, _ := url.Parse(raw); return u.Host }
	onMain(func() {
		cfg, _ := config.Load(dir)
		a := NewApp(cfg, tokens, log)
		a.Start()
		defer func() {
			for _, sv := range a.servers {
				if sv.main != nil {
					sv.main.Close()
				}
			}
			a.shell.w.Close()
		}()
		a.submitLogin(a.adding, LoginForm{Server: srv.URL, LoginID: "alice", Password: mmfake.Password})
		if !waitFor(t, "signed in", func() bool { return a.active != nil && a.active.main != nil && a.active.main.connectedOnce }) {
			return
		}
		m := a.active.main
		if !waitFor(t, "sidebar", func() bool { return len(m.items) > 0 }) {
			return
		}
		alerts := channelByName(m, "ops", "alerts")
		p := f.Post(&model.Post{ChannelId: alerts.Id, UserId: f.UserID("bob"), Message: "по ссылке из браузера"})

		a.OpenLink("mattermost://"+host(srv.URL)+"/ops/pl/"+p.Id, "token-123")
		if !waitFor(t, "post from the browser link", func() bool { return m.current == alerts.Id && m.feed.highlight == p.Id }) {
			return
		}

		a.OpenLink("mattermost://mm.example.org/dev/channels/town-square", "")
		if a.adding == nil || a.adding.server.Text() != "mm.example.org" {
			t.Error("unknown server: no «add server» page with its address")
		}
	})
}
