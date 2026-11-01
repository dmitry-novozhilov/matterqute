package ui

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/markdown"
	"matterqute/internal/mm"
	"matterqute/internal/mmfake"
)

func TestFilesAndMarkdown(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	upload := filepath.Join(t.TempDir(), "заметки.txt")
	if err := os.WriteFile(upload, []byte("тестовый файл\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	onMain(func() { testFilesAndMarkdown(t, fake, s, upload) })
}

func testFilesAndMarkdown(t *testing.T, _ *mmfake.Server, s *mm.Session, upload string) {
	m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
	defer m.Close()
	m.Show()
	if !waitFor(t, "sidebar", func() bool { return len(m.items) > 0 }) {
		return
	}
	backend := channelByName(m, "dev", "backend")
	m.selectChannel(backend.Id)
	if !waitFor(t, "backend posts", func() bool { return len(m.feed.items) >= 5 }) {
		return
	}

	var img *model.FileInfo
	var md *postItem
	for _, it := range m.feed.items {
		if it.post.Metadata != nil && len(it.post.Metadata.Files) > 0 && isImage(it.post.Metadata.Files[0]) {
			img = it.post.Metadata.Files[0]
		}
		if strings.Contains(it.post.Message, ":partyparrot:") {
			md = it
		}
	}
	if img == nil || md == nil {
		t.Errorf("seeded image (%v) or markdown post (%v) not in backend", img != nil, md != nil)
		return
	}
	html := func(id string) string {
		if i := m.feed.index(id); i >= 0 {
			return m.feed.items[i].html
		}
		return ""
	}
	imgPost := ""
	for _, it := range m.feed.items {
		if it.post.Metadata != nil && len(it.post.Metadata.Files) > 0 && it.post.Metadata.Files[0] == img {
			imgPost = it.post.Id
		}
	}
	waitFor(t, "image preview in feed", func() bool { return strings.Contains(html(imgPost), "mm-img:"+img.Id) })
	waitFor(t, "custom emoji rendered", func() bool { return strings.Contains(html(md.post.Id), `<img src="file://`) })
	body := html(md.post.Id)
	for _, want := range []string{"font-size:15px;font-weight:600", "☑", `<table cellspacing="1"`, "white-space:pre-wrap", "mm-channel:town-square", "👍"} {
		if !strings.Contains(body, want) {
			t.Errorf("markdown post lacks %q:\n%s", want, body)
		}
	}

	m.feed.ScrollToPost(md.post.Id)
	shot(t, m, "markdown-post")

	// GIF крутится: QMovie запущен, кадры идут в ресурс превью.
	var gifID string
	for _, it := range m.feed.items {
		if it.post.Metadata != nil && len(it.post.Metadata.Files) > 0 && it.post.Metadata.Files[0].MimeType == "image/gif" {
			gifID = it.post.Metadata.Files[0].Id
		}
	}
	if gifID == "" {
		t.Error("seeded GIF not in backend")
	} else {
		var frames []int
		waitFor(t, "GIF animating", func() bool {
			mv := m.feed.movies[gifID]
			if mv == nil || !mv.IsValid() || mv.State() != qt.QMovie__Running {
				return false
			}
			if n := mv.CurrentFrameNumber(); len(frames) == 0 || frames[len(frames)-1] != n {
				frames = append(frames, n)
			}
			return len(frames) >= 3 && mv.FrameCount() == 8
		})
	}

	// Черновик остаётся в своём канале.
	m.composer.edit.SetPlainText("черновик backend")
	town := channelByName(m, "dev", "town-square")
	m.handleLink(markdown.ChannelLinkScheme + "town-square")
	if m.current != town.Id {
		t.Errorf("~town-square link opened %s, want %s", m.current, town.Id)
	}
	if got := m.composer.edit.ToPlainText(); got != "" {
		t.Errorf("draft leaked into town-square: %q", got)
	}
	m.selectChannel(backend.Id)
	if got := m.composer.edit.ToPlainText(); got != "черновик backend" {
		t.Errorf("backend draft = %q after coming back", got)
	}
	m.composer.edit.Clear()
	waitFor(t, "backend posts again", func() bool { return len(m.feed.items) >= 5 })
	shot(t, m, "markdown-files")

	// Загрузка и отправка файла.
	m.composer.AddFiles([]string{upload})
	if !waitFor(t, "upload", func() bool { return len(m.composer.files) == 1 && m.composer.files[0].fileID != "" }) {
		return
	}
	m.composer.edit.SetPlainText("Заметки со встречи")
	shot(t, m, "composer-attachment")
	m.composer.send()
	waitFor(t, "post with file", func() bool {
		for _, it := range m.feed.items {
			if it.post.Metadata != nil && len(it.post.Metadata.Files) == 1 && it.post.Metadata.Files[0].Name == "заметки.txt" {
				return strings.Contains(it.html, saveLink+it.post.Metadata.Files[0].Id)
			}
		}
		return false
	})
	if len(m.composer.files) != 0 {
		t.Errorf("attachments left in composer after send: %v", len(m.composer.files))
	}

	// Вложение бота в alerts.
	alerts := channelByName(m, "ops", "alerts")
	m.selectChannel(alerts.Id)
	waitFor(t, "bot attachment", func() bool {
		for _, it := range m.feed.items {
			if strings.Contains(it.html, "DiskSpaceLow") && strings.Contains(it.html, `bgcolor="`+Pal().Dang+`"`) {
				return true
			}
		}
		return false
	})
	shot(t, m, "bot-attachment")
}

func TestFitSizeAndCanOpen(t *testing.T) {
	for _, c := range []struct{ w, h, ww, wh int }{
		{640, 360, 360, 202}, {100, 50, 100, 50}, {300, 1200, 60, 240}, {0, 0, 360, 160},
	} {
		if w, h := fitSize(c.w, c.h, 360, 240); w != c.ww || h != c.wh {
			t.Errorf("fitSize(%d, %d) = %d×%d, want %d×%d", c.w, c.h, w, h, c.ww, c.wh)
		}
	}
	for name, want := range map[string]bool{"a.PDF": true, "x.png": true, "run.sh": false, "app.desktop": false, "page.html": false, "pic.svg": false, "noext": false} {
		if got := canOpen(&model.FileInfo{Name: name}); got != want {
			t.Errorf("canOpen(%q) = %v, want %v", name, got, want)
		}
	}
}
