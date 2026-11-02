package ui

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/config"
	"matterqute/internal/mmfake"
)

// Окно закрыли — место и размер в config.json; следующий запуск их восстановил.
func TestAppRemembersWindow(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	onMain(func() {
		cfg, err := config.Load(dir)
		if err != nil {
			t.Error(err)
			return
		}
		a := NewApp(cfg, nil, log) // без серверов: только страница входа, токены не нужны
		a.Start()
		// Экран offscreen — 800×600: окно должно на него помещаться.
		a.shell.w.SetMinimumSize2(100, 100)
		a.shell.w.Resize(640, 480)
		a.shell.w.Move(30, 20)
		settle()
		a.shell.w.Close()

		cfg2, err := config.Load(dir)
		if err != nil || len(cfg2.Window.Geometry) == 0 {
			t.Errorf("geometry not saved on close: %v, %d bytes", err, len(cfg2.Window.Geometry))
			return
		}
		b := NewApp(cfg2, nil, log)
		b.shell.w.SetMinimumSize2(100, 100)
		b.Start()
		defer b.shell.w.Close()
		settle()
		// Рамку окна до показа Qt угадывает, место может съехать на её толщину.
		near := func(got, want int) bool { return abs(got-want) <= 4 }
		if sz, pos := b.shell.w.Size(), b.shell.w.Pos(); sz.Width() != 640 || sz.Height() != 480 || !near(pos.X(), 30) || !near(pos.Y(), 20) {
			t.Errorf("next start: window %dx%d at %d,%d; want 640x480 at 30,20", sz.Width(), sz.Height(), pos.X(), pos.Y())
		}
	})
}

// Два сервера: вход на оба, переключение, счётчик неактивного в меню,
// повторный запуск входит на оба, выход с одного не трогает другой.
func TestAppServers(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	type fakeSrv struct {
		fake *mmfake.Server
		url  string
	}
	var fakes []fakeSrv
	for range 2 {
		f := mmfake.New(log)
		srv := httptest.NewServer(f)
		t.Cleanup(srv.Close)
		fakes = append(fakes, fakeSrv{f, srv.URL})
	}
	dir := t.TempDir()
	tokens := config.FileTokens(filepath.Join(dir, "tokens.json"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))

	onMain(func() {
		cfg, _ := config.Load(dir)
		a := NewApp(cfg, tokens, log)
		badge := &fakeBadge{}
		a.SetLauncher(badge)
		a.Start()
		defer func() {
			for _, sv := range a.servers {
				if sv.main != nil {
					sv.main.Close()
				}
			}
			a.shell.w.Close()
		}()
		login := func(url string) bool {
			if a.adding == nil {
				t.Error("no «add server» login page")
				return false
			}
			a.submitLogin(a.adding, LoginForm{Server: url, LoginID: "alice", Password: mmfake.Password})
			return waitFor(t, "signed in to "+url, func() bool {
				return a.active != nil && a.active.url == url && a.active.main != nil && a.active.main.connectedOnce
			})
		}
		// Первый запуск: сразу «добавить сервер», потом ещё один из меню.
		if !login(fakes[0].url) {
			return
		}
		a.shell.OnAddServer()
		if !login(fakes[1].url) {
			return
		}
		if len(a.servers) != 2 || len(cfg.Servers) != 2 || cfg.Active != fakes[1].url {
			t.Errorf("after two logins: %d servers, config %d, active %s", len(a.servers), len(cfg.Servers), cfg.Active)
		}
		menu := func() []string {
			var out []string
			for _, act := range a.shell.serverMenu.Actions() {
				if act.IsCheckable() {
					out = append(out, fmt.Sprintf("%s|%v", act.Text(), act.IsChecked()))
				}
			}
			return out
		}
		host := func(i int) string { return hostOf(fakes[i].url) }
		// У первого (неактивного) — счётчик засеянных упоминаний, у текущего — нет.
		first := a.servers[0].counts.Mentions
		if got := strings.Join(menu(), ";"); first == 0 || got != fmt.Sprintf("%s   %d|false;%s|true", host(0), first, host(1)) {
			t.Errorf("server menu %q (first server has %d mentions)", got, first)
		}

		// Переключились на первый; упоминание на втором — счётчик в меню и точка.
		a.shell.OnSelectServer(0)
		if a.active != a.servers[0] || a.shell.stack.CurrentWidget().UnsafePointer() != a.servers[0].main.Widget().UnsafePointer() {
			t.Error("Ctrl+1 did not show the first server")
		}
		second := a.servers[1]
		before := second.counts.Mentions
		bob := second.main.st.UserByName("bob")
		backend := channelByName(second.main, "dev", "backend")
		fakes[1].fake.Post(&model.Post{ChannelId: backend.Id, UserId: bob.Id, Message: "@alice со второго сервера"})
		waitFor(t, "mention on the inactive server", func() bool { return second.counts.Mentions == before+1 })
		if got := menu(); len(got) != 2 || !strings.HasPrefix(got[1], fmt.Sprintf("%s   %d|false", host(1), before+1)) {
			t.Errorf("server menu after mention %q", got)
		}
		if title := a.shell.serverMenu.Title(); title != "&Сервер •" {
			t.Errorf("server menu title %q, want a dot for news elsewhere", title)
		}
		// Значок в доке — сумма по всем серверам, как и трей.
		if want := a.servers[0].counts.Mentions + a.servers[1].counts.Mentions; badge.last != want {
			t.Errorf("dock badge %d, want %d", badge.last, want)
		}

		// Повторный запуск: входим на оба по токенам, показан последний открытый.
		cfg2, _ := config.Load(dir)
		b := NewApp(cfg2, tokens, log)
		b.Start()
		defer func() {
			for _, sv := range b.servers {
				if sv.main != nil {
					sv.main.Close()
				}
			}
			b.shell.w.Close()
		}()
		if !waitFor(t, "both servers resumed", func() bool {
			return len(b.servers) == 2 && b.servers[0].main != nil && b.servers[1].main != nil
		}) {
			return
		}
		if b.active != b.servers[0] {
			t.Errorf("restart shows %s, want the last opened %s", b.active.url, fakes[0].url)
		}

		// Выход с текущего: его страница входа, второй сервер работает дальше.
		b.shell.OnLogout()
		if first := b.servers[0]; first.main != nil || first.login == nil || b.shell.stack.CurrentWidget().UnsafePointer() != first.login.Widget().UnsafePointer() {
			t.Error("logout did not show the login page of the current server")
		}
		if b.servers[1].main == nil {
			t.Error("logout from one server closed the other")
		}
		if _, err := tokens.Get(fakes[0].url); !errors.Is(err, config.ErrNoToken) {
			t.Errorf("token of the logged out server kept: %v", err)
		}
		if _, err := tokens.Get(fakes[1].url); err != nil {
			t.Errorf("token of the other server lost: %v", err)
		}

		// Удалить текущий (из него вышли): остался второй, он и на экране.
		b.shell.OnRemoveServer()
		labelled(b.removeDialog.w.QObject, "Удалить").Click()
		if len(b.servers) != 1 || b.active != b.servers[0] || b.active.url != fakes[1].url || b.active.main == nil {
			t.Fatalf("after removing the first server: %d servers, active %v", len(b.servers), b.active)
		}
		if b.shell.stack.CurrentWidget().UnsafePointer() != b.active.main.Widget().UnsafePointer() {
			t.Error("the remaining server is not on screen")
		}
		if c, _ := config.Load(dir); len(c.Servers) != 1 || c.Active != fakes[1].url {
			t.Errorf("config after removal: %+v active %s", c.Servers, c.Active)
		}
		// Удалить последний: страница добавления, токен стёрт, настройки пусты.
		b.shell.OnRemoveServer()
		labelled(b.removeDialog.w.QObject, "Удалить").Click()
		if len(b.servers) != 0 || b.active != nil || b.adding == nil {
			t.Errorf("after removing the last server: %d servers, adding page %v", len(b.servers), b.adding != nil)
		}
		if _, err := tokens.Get(fakes[1].url); !errors.Is(err, config.ErrNoToken) {
			t.Errorf("token of the removed server kept: %v", err)
		}
		if c, _ := config.Load(dir); len(c.Servers) != 0 || c.Active != "" {
			t.Errorf("config after removing all: %+v active %q", c.Servers, c.Active)
		}
	})
}

type fakeBadge struct{ last int64 }

func (b *fakeBadge) SetCount(n int64) error {
	b.last = n
	return nil
}

// Закрытое и удалённое окно сервера не остаётся в подписчиках темы: смена темы
// после выхода из аккаунта не должна лезть в удалённые виджеты.
func TestMainWindowCloseUnsubscribes(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		before := len(style.listeners)
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		if len(style.listeners) == before {
			t.Error("main window did not subscribe to theme changes: test is meaningless")
		}
		m.Close()
		m.Widget().DeleteLater()
		settle()
		if n := len(style.listeners); n != before {
			t.Errorf("%d theme listeners left after closing the main window", n-before)
		}
		SetThemeMode(ThemeDark)
		SetThemeMode(ThemeLight)
	})
}
