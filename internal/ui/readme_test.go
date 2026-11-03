package ui

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/config"
	"matterqute/internal/mmfake"
)

// Скриншоты для README: всё окно (меню, строка состояния) на фейковом
// сервере с демо-перепиской, светлая и тёмная тема; в строке состояния
// вместо адреса фейка — chat.example.com. Только по запросу:
//
//	MATTERQUTE_README_SHOTS=$PWD/docs/screenshots go test ./internal/ui -run TestReadmeScreenshots
//
// Чёткие для HiDPI — с QT_SCALE_FACTOR=2.
func TestReadmeScreenshots(t *testing.T) {
	dir := os.Getenv("MATTERQUTE_README_SHOTS")
	if dir == "" {
		t.Skip("MATTERQUTE_README_SHOTS не задан")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fake := mmfake.New(log)
	fake.NameFormat = model.ShowFullName
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	cfgDir := t.TempDir()
	tokens := config.FileTokens(filepath.Join(cfgDir, "tokens.json"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(cfgDir, "cache"))

	onMain(func() {
		defer SetThemeMode(ThemeLight)
		defer SetFormatBar(false)
		cfg, _ := config.Load(cfgDir)
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
		a.shell.w.Resize(1280, 800)
		a.submitLogin(a.adding, LoginForm{Server: srv.URL, LoginID: "alice", Password: mmfake.Password})
		if !waitFor(t, "signed in", func() bool { return a.active != nil && a.active.main != nil && a.active.main.connectedOnce }) {
			return
		}
		m := a.active.main
		save := func(name string) {
			a.shell.server.SetText("chat.example.com") // вместо 127.0.0.1:порт фейка
			settleFor(800 * time.Millisecond)
			if path := filepath.Join(dir, name); !a.shell.w.Grab().Save(path) {
				t.Errorf("save %s", path)
			}
		}
		user := fake.UserID
		if path := filepath.Join(dir, "icon.png"); !appIconPixmap(128).Save(path) {
			t.Errorf("save %s", path)
		}

		// Светлая: обсуждение в backend — код с подсветкой, реакции, тред,
		// «Важно», упоминание.
		backend := channelByName(m, "dev", "backend").Id
		m.selectChannel(backend)
		code := fake.Post(&model.Post{ChannelId: backend, UserId: user("bob"), Message: "Нашёл, где теряем время: под нагрузкой все ждут один мьютекс.\n" +
			"```go\nfunc (c *Cache) Get(key string) (Session, bool) {\n\tc.mu.RLock()\n\tdefer c.mu.RUnlock()\n" +
			"\ts, ok := c.items[key]\n\tif !ok || s.Expired(time.Now()) {\n\t\treturn Session{}, false\n\t}\n\treturn s, true\n}\n```\n" +
			"Предлагаю шардировать по ключу — набросал PR."})
		for _, r := range []struct{ who, emoji string }{{"carol", "+1"}, {"dave", "+1"}, {"alice", "eyes"}, {"carol", "rocket"}} {
			fake.React(user(r.who), code.Id, r.emoji, true)
		}
		fake.Post(&model.Post{ChannelId: backend, UserId: user("carol"), RootId: code.Id, Message: "16 шардов хватит? Ключи и так равномерные."})
		fake.Post(&model.Post{ChannelId: backend, UserId: user("bob"), RootId: code.Id, Message: "Да, на профиле ожидание мьютекса ушло почти в ноль :tada:"})
		fake.Post(&model.Post{ChannelId: backend, UserId: user("alice"), RootId: code.Id, Message: "Отлично, ревьюю после обеда."})
		release := &model.Post{ChannelId: backend, UserId: user("carol"), Message: "Выкатываем **2.5** сегодня в 18:00. Кто против — пишите в тред."}
		release.Metadata = &model.PostMetadata{Priority: &model.PostPriority{Priority: model.NewPointer(priorityImportant)}}
		fake.Post(release)
		last := fake.Post(&model.Post{ChannelId: backend, UserId: user("dave"), Message: "@alice глянешь миграцию до релиза? Там новый индекс на `sessions`."})
		if !waitFor(t, "demo posts", func() bool { return m.feed.Has(last.Id) }) {
			return
		}
		m.openThread(code.Id)
		if !waitFor(t, "thread", func() bool { return len(m.panel.feed.items) == 4 }) {
			return
		}
		toBottom(m.feed)
		save("light.png")

		// Тёмная: алерт Grafana с кнопками, панель реакций, «печатает…»,
		// панель оформления у поля ввода.
		SetThemeMode(ThemeDark)
		SetFormatBar(true)
		m.closeThread()
		alerts := channelByName(m, "ops", "alerts").Id
		m.selectChannel(alerts)
		fake.Post(&model.Post{ChannelId: alerts, UserId: user("dave"), Message: "Смотрю. Похоже, разрослись WAL-логи после вчерашней миграции."})
		note := fake.Post(&model.Post{ChannelId: alerts, UserId: user("carol"), Message: "Почистил архив WAL, свободно **41%**. Алерт должен погаснуть через пару минут."})
		if !waitFor(t, "alert posts", func() bool { return m.feed.Has(note.Id) }) {
			return
		}
		fake.React(user("dave"), note.Id, "white_check_mark", true)
		fake.React(user("bob"), note.Id, "pray", true)
		fake.Typing(user("bob"), alerts, "")
		if !waitFor(t, "typing", func() bool { return m.head.note != "" }) {
			return
		}
		toBottom(m.feed)
		settleFor(300 * time.Millisecond)
		hoverPost(m.feed, m.feed.index(note.Id))
		save("dark.png")
	})
}

func toBottom(f *Feed) {
	sb := f.view.VerticalScrollBar()
	sb.SetValue(sb.Maximum())
}
