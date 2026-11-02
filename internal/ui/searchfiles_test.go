package ui

import (
	"io"
	"log/slog"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// Поиск: вкладка «Файлы» — файлы во всех каналах (с каналом у каждого),
// запрос с in:канал — по командам; смена вкладки — тот же запрос заново;
// у списков (закреплённые и т. п.) вкладок нет.
func TestSearchFiles(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		sp := m.search
		m.openSearch()
		sp.field.SetText("ext:pdf")
		sp.filesTab.Click()
		if !waitFor(t, "pdf found", func() bool { return sp.files.list.Count() == 1 }) {
			return
		}
		for _, fi := range sp.files.files {
			if fi.Name != "load-report.pdf" {
				t.Errorf("found %q", fi.Name)
			}
		}
		if !sp.files.list.IsVisible() || sp.feed.Widget().IsVisible() {
			t.Error("files tab does not show the file list")
		}
		shot(t, m, "search-files")
		// in:канал — по каждой команде отдельно.
		sp.field.SetText("in:backend схема")
		m.runSearch("in:backend схема")
		if !waitFor(t, "in:backend", func() bool { return sp.files.list.Count() == 1 && sp.info.Text() == "Найдено: 1 файл" }) {
			return
		}
		// Сообщения — тот же запрос.
		sp.field.SetText("деплой")
		sp.postsTab.Click()
		if !waitFor(t, "posts tab", func() bool { return sp.feed.Widget().IsVisible() && len(sp.feed.items) > 0 }) {
			return
		}
		// Списки — без вкладок.
		m.toggleList(listSaved)
		if sp.modes.IsVisible() {
			t.Error("tabs shown for the saved list")
		}
		m.openSearch()
		if !sp.modes.IsVisible() {
			t.Error("tabs hidden in search")
		}
	})
}
