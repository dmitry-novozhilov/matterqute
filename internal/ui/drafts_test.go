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

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/mm"
	"matterqute/internal/mmfake"
)

// Черновики на сервере: набранное в поле канала и треда сохраняется там,
// черновик из другого клиента приходит в поле, всё переживает закрытие окна;
// стёртое — удаляется.
func TestDraftsOnServer(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	me := s.Me.Id
	var town, backend, root string
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		r := openTown(t, m)
		if r == nil {
			return
		}
		town, root = m.current, r.Id
		backend = channelByName(m, "dev", "backend").Id

		m.composer.edit.InsertPlainText("недописанное в канале")
		if !waitFor(t, "channel draft on the server", func() bool { return fake.Draft(me, town, "") == "недописанное в канале" }) {
			return
		}
		m.openThread(root)
		if !waitFor(t, "thread", func() bool { return m.thread != nil && m.thread.tl.Loaded }) {
			return
		}
		m.panel.composer.edit.InsertPlainText("ответ потом")
		if !waitFor(t, "thread draft on the server", func() bool { return fake.Draft(me, town, root) == "ответ потом" }) {
			return
		}

		// Ушли из канала — в сайдбаре карандаш; черновик из «браузера» — в поле.
		m.selectChannel(backend)
		if !m.composer.HasDraft(town) {
			t.Error("left channel has no draft mark")
		}
		fake.SetDraft(me, backend, "", "из браузера")
		if !waitFor(t, "draft from another client in the field", func() bool { return m.composer.DraftText(backend) == "из браузера" }) {
			return
		}
		// Здесь дописали, а «браузер» прислал своё: правка здесь главнее и уходит на сервер.
		m.composer.edit.MoveCursor(qt.QTextCursor__End)
		m.composer.edit.InsertPlainText(" и дописал")
		fake.SetDraft(me, backend, "", "снова из браузера")
		waitFor(t, "local edit wins", func() bool {
			return m.composer.DraftText(backend) == "из браузера и дописал" && fake.Draft(me, backend, "") == "из браузера и дописал"
		})
	})
	if t.Failed() {
		return
	}

	// Новое окно (перезапуск): черновики — с сервера.
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		openTown(t, m)
		if !waitFor(t, "channel draft after restart", func() bool { return m.composer.DraftText(town) == "недописанное в канале" }) {
			return
		}
		if !m.composer.HasDraft(backend) || m.composer.DraftText(backend) != "из браузера и дописал" {
			t.Errorf("backend draft from the server not kept for later: %q", m.composer.DraftText(backend))
		}
		m.openThread(root)
		waitFor(t, "thread draft after restart", func() bool { return m.panel.composer.DraftText(root) == "ответ потом" })

		// Стёрли — на сервере черновика больше нет.
		m.composer.setBlocks(nil)
		waitFor(t, "cleared draft deleted", func() bool { return fake.Draft(me, town, "") == "" })
	})
}

// Сервер черновики не хранит — они в файле и тоже переживают перезапуск.
func TestDraftsInFile(t *testing.T) {
	fake := mmfake.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	fake.CollapsedThreads = model.CollapsedThreadsAlwaysOn
	fake.NoSyncedDrafts = true
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	s, err := mm.Login(context.Background(), srv.URL, "alice", mmfake.Password, "")
	if err != nil {
		t.Fatalf("login to fake: %v", err)
	}
	path := filepath.Join(t.TempDir(), "drafts", "x.json")
	newWindow := func() *MainWindow {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		m.drafts.path = path
		return m
	}
	onMain(func() {
		m := newWindow()
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		m.composer.edit.InsertPlainText("только у себя")
		waitFor(t, "draft in the file", func() bool {
			data, _ := os.ReadFile(path)
			return strings.Contains(string(data), "только у себя") && !m.drafts.server
		})
	})
	onMain(func() {
		m := newWindow()
		defer m.Close()
		openTown(t, m)
		waitFor(t, "draft from the file after restart", func() bool { return m.composer.DraftText(m.current) == "только у себя" })
	})
}

// Окно из снимка: набранное до того, как пришёл сайдбар с сервера (и
// сохранённое пока в файл), сверка с сервером не стирает — наоборот, оно
// уходит на сервер.
func TestDraftTypedBeforeSidebar(t *testing.T) {
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
		m.Close()
		st, err := readState(statePath(cacheDir, s.ServerURL))
		if err != nil || st == nil {
			t.Errorf("read state: %v", err)
			return
		}
		m = NewMainWindow(mm.Offline(s.ServerURL, s.Token(), st.Me), log, town, cacheDir)
		defer m.Close()
		m.restore = st
		m.w.Show()
		m.Start()
		// Ни одного ответа сервера ещё нет: набираем и «проходит» 0,8 с.
		m.composer.edit.InsertPlainText("набрал до сайдбара")
		m.drafts.save(false)
		if m.drafts.server {
			t.Error("storage chosen before the sidebar")
		}
		if !waitFor(t, "drafts synced with the server", func() bool { return m.sidebarOK && m.drafts.server && m.drafts.loaded }) {
			return
		}
		waitFor(t, "typed draft on the server", func() bool { return fake.Draft(s.Me.Id, town, "") == "набрал до сайдбара" })
		if got := m.composer.DraftText(town); got != "набрал до сайдбара" {
			t.Errorf("draft typed before the sidebar: %q", got)
		}
	})
}
