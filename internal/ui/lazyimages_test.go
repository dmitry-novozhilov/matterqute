package ui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Картинки ленты — по требованию: скрытая лента держит заглушки, на экране —
// раскодированы видимые, ушла с экрана — отпущены.
func TestLazyImages(t *testing.T) {
	needMalloc(t)
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		m.w.Resize(900, 900)
		if openTown(t, m) == nil {
			return
		}
		backendID := channelByName(m, "dev", "backend").Id
		var backend *Feed
		if !waitFor(t, "backend built in the background", func() bool {
			backend = m.feeds[backendID]
			return backend != nil && backend.ready && !backend.hasWork() && len(backend.lazy) >= 3
		}) {
			return
		}
		gif := ""
		for _, it := range backend.items {
			if md := it.post.Metadata; md != nil && len(md.Files) > 0 && md.Files[0].MimeType == "image/gif" {
				gif = md.Files[0].Id
			}
		}
		if len(backend.loaded) != 0 || backend.movies[gif] != nil {
			t.Errorf("hidden feed: %d pictures decoded, GIF movie %v", len(backend.loaded), backend.movies[gif] != nil)
		}

		// На экран — видимые раскодированы (на это и растёт куча), GIF крутится.
		grew := mallocGrowth(func() {
			m.selectChannel(backendID)
			settleFor(300 * time.Millisecond)
		})
		if len(backend.loaded) == 0 || backend.movies[gif] == nil {
			t.Errorf("feed on screen: %d pictures decoded, GIF movie %v", len(backend.loaded), backend.movies[gif] != nil)
		}
		var want int64 // точки раскодированных: вдвое крупнее, по 4 байта
		for name := range backend.loaded {
			sz := backend.resources[name]
			want += int64(sz[0]*resourceDPR) * int64(sz[1]*resourceDPR) * 4
		}
		if want < 1<<20 || grew < want/2 {
			t.Errorf("%d pictures on screen (%.1f MB of pixels) took %.1f MB", len(backend.loaded), mb(want), mb(grew))
		}
		// Ушла с экрана — отпущены.
		freed := -mallocGrowth(func() {
			m.selectChannel(channelByName(m, "dev", "town-square").Id)
			settle()
		})
		if len(backend.loaded) != 0 || backend.movies[gif] != nil {
			t.Errorf("hidden again: %d pictures decoded, GIF movie %v", len(backend.loaded), backend.movies[gif] != nil)
		}
		if freed < want/2 {
			t.Errorf("hiding the feed freed %.1f MB of %.1f MB of its pictures", mb(freed), mb(want))
		}
	})
}

// Длинная лента с картинками: далёкие от видимого места (дальше lazyKeep
// экранов) отпускаются, видимые — раскодированы.
func TestLazyImagesFar(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		m.w.Resize(900, 600)
		m.Show()
		if !waitFor(t, "sidebar", func() bool { return len(m.items) > 0 }) {
			return
		}
		ch := channelByName(m, "dev", "backend")
		const n = 14
		for i := range n {
			img := image.NewRGBA(image.Rect(0, 0, 640, 360))
			for y := range 360 {
				for x := range 640 {
					img.Set(x, y, color.RGBA{uint8(i * 15), uint8(x / 3), uint8(y / 2), 255})
				}
			}
			var b bytes.Buffer
			png.Encode(&b, img)
			fis, _, err := s.Client.UploadFile(context.Background(), b.Bytes(), ch.Id, fmt.Sprintf("shot%d.png", i))
			if err != nil {
				t.Errorf("upload: %v", err)
				return
			}
			if _, _, err := s.Client.CreatePost(context.Background(), &model.Post{ChannelId: ch.Id, Message: fmt.Sprint("снимок ", i), FileIds: []string{fis.FileInfos[0].Id}}); err != nil {
				t.Errorf("post: %v", err)
				return
			}
		}
		m.selectChannel(ch.Id)
		f := m.feeds[ch.Id]
		if !waitFor(t, "screenshots in the feed", func() bool {
			return f != nil && f.ready && len(f.items) >= n && func() bool {
				for _, it := range f.items {
					if it.post.Message == fmt.Sprint("снимок ", n-1) {
						return len(it.images) == 1
					}
				}
				return false
			}()
		}) {
			return
		}
		drained(f)
		visibleLoaded := func() bool {
			lo, hi, ok := f.visibleRange()
			if !ok {
				return false
			}
			for _, it := range f.items[lo : hi+1] {
				for _, name := range it.images {
					if !f.loaded[name] {
						return false
					}
				}
			}
			return true
		}
		bottom := map[string]bool{}
		if !waitFor(t, "pictures at the bottom decoded", visibleLoaded) {
			return
		}
		for name := range f.loaded {
			bottom[name] = true
		}
		// Наверх: внизу — дальше двух экранов, отпущены (через секунду), вверху — раскодированы.
		f.ScrollToTop()
		settle()
		if !waitFor(t, "far pictures released", func() bool {
			for name := range bottom {
				if f.loaded[name] {
					return false
				}
			}
			return visibleLoaded()
		}) {
			t.Logf("loaded %d of %d", len(f.loaded), len(f.lazy))
		}
	})
}
