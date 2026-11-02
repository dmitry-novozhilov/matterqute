package ui

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/config"
	"matterqute/internal/mm"
	"matterqute/internal/mmfake"
)

// Окно из снимка: сайдбар и лента открытого канала — сразу, до ответа
// сервера; написанное, пока окно было закрыто, приходит сверкой.
func TestStateRestore(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	cacheDir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	onMain(func() {
		m := NewMainWindow(s, log, "", cacheDir)
		if openTown(t, m) == nil {
			m.Close()
			return
		}
		town := m.current
		backend := channelByName(m, "dev", "backend").Id
		if !waitFor(t, "backend preloaded", func() bool { tl := m.st.Timeline(backend); return tl != nil && tl.Loaded && !tl.Stale }) {
			m.Close()
			return
		}
		m.Close()
		path := statePath(cacheDir, s.ServerURL)
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("state file: %v, %v", fi, err)
			return
		}

		// Пока окна нет, bob пишет в Town Square.
		bob := fake.UserID("bob")
		fresh := fake.Post(&model.Post{ChannelId: town, UserId: bob, Message: "пока тебя не было"})

		st, err := readState(path)
		if err != nil || st == nil {
			t.Errorf("read state: %v", err)
			return
		}
		m = NewMainWindow(mm.Offline(s.ServerURL, s.Token(), st.Me), log, town, cacheDir)
		defer m.Close()
		m.restore = st
		m.w.Show()
		m.Start()
		// Ещё ни одного события из сети: всё — из снимка.
		if !m.restored || m.current != town || len(m.items) == 0 || m.feed == nil || !m.feed.Has(m.st.Timeline(town).Posts[0].Id) {
			t.Errorf("not restored synchronously: restored %v, current %q, %d items", m.restored, m.current, len(m.items))
			return
		}
		if m.feed.Has(fresh.Id) {
			t.Error("post written while closed is in the snapshot")
		}
		if !waitFor(t, "new post after resync", func() bool { return m.feed.Has(fresh.Id) && m.sidebarOK }) {
			return
		}
		// Другие ленты из снимка готовы, не дожидаясь сервера.
		if f := m.feeds[backend]; f == nil || !f.ready {
			t.Error("backend feed not built from the snapshot")
		}

		// Выход из аккаунта стирает снимок, и закрытие окна его не вернёт.
		m.dropState()
		m.Close()
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("state survived logout: %v", err)
		}
	})
}

// Запуск со снимком: сервер недоступен — окно всё равно из снимка; токен
// отозван — страница входа, снимок стёрт.
func TestAppStartsFromState(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fake := mmfake.New(log)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	tokens := config.FileTokens(filepath.Join(dir, "tokens.json"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))

	onMain(func() {
		cfg, _ := config.Load(dir)
		a := NewApp(cfg, tokens, log)
		a.shell.quitApp = func() {} // настоящий quit остановил бы цикл событий всех тестов
		a.Start()
		a.submitLogin(a.adding, LoginForm{Server: srv.URL, LoginID: "alice", Password: mmfake.Password})
		if !waitFor(t, "signed in", func() bool { return a.active != nil && a.active.main != nil && a.active.main.sidebarOK }) {
			a.shell.w.Close()
			return
		}
		a.shell.Quit()
		cacheBase, _ := config.CacheDir()
		path := statePath(cacheBase, srv.URL)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("no state after quit: %v", err)
			return
		}

		// Сервер лёг: окно — из снимка, связь ждёт.
		srv.Config.Handler = httpDown{}
		cfg, _ = config.Load(dir)
		b := NewApp(cfg, tokens, log)
		b.shell.quitApp = func() {}
		b.Start()
		if !waitFor(t, "window from state while server is down", func() bool {
			sv := b.active
			return sv != nil && sv.main != nil && sv.main.restored && len(sv.main.items) > 0
		}) {
			b.shell.w.Close()
			return
		}
		b.shell.Quit()

		// Сервер жив, но токен отозвали: страница входа, снимок прочь.
		srv.Config.Handler = fake
		token, _ := tokens.Get(srv.URL)
		mm.Offline(srv.URL, token, &model.User{}).Logout(context.Background())
		cfg, _ = config.Load(dir)
		c := NewApp(cfg, tokens, log)
		c.Start()
		defer c.shell.w.Close()
		if !waitFor(t, "login page after rejected token", func() bool {
			sv := c.active
			return sv != nil && sv.main == nil && sv.login != nil
		}) {
			return
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("state survived a rejected token: %v", err)
		}
	})
}

// httpDown — сервер «лежит»: на всё 502.
type httpDown struct{}

func (httpDown) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "down", http.StatusBadGateway)
}
