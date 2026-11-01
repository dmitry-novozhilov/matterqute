package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/multimedia"
	"github.com/mattermost/mattermost/server/public/model"
)

func TestFormatDuration(t *testing.T) {
	for ms, want := range map[int64]string{0: "0:00", 7400: "0:07", 64000: "1:04", 3723000: "1:02:03"} {
		if got := formatDuration(ms); got != want {
			t.Errorf("formatDuration(%d) = %q, want %q", ms, got, want)
		}
	}
}

// Ролик в ленте: превью с кадром и длительностью, клик — плеер, который играет.
func TestVideo(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
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
		var fi *model.FileInfo
		var post *model.Post
		for _, it := range m.feed.items {
			if it.post.Metadata != nil && len(it.post.Metadata.Files) > 0 && isVideo(it.post.Metadata.Files[0]) {
				fi, post = it.post.Metadata.Files[0], it.post
			}
		}
		if fi == nil {
			t.Error("seeded video not in backend")
			return
		}
		html := func() string { return m.feed.items[m.feed.index(post.Id)].html }
		if !strings.Contains(html(), videoLink+fi.Id) {
			t.Errorf("video post has no player link:\n%s", html())
		}

		// Маленький ролик лента разбирает сама: кадр, длительность, размер.
		if !waitFor(t, "video probed", func() bool { _, ok := m.assets.Video(fi.Id); return ok }) {
			return
		}
		info, _ := m.assets.Video(fi.Id)
		if info.Width != 320 || info.Height != 180 || info.Duration < 1900 || info.Duration > 2100 {
			t.Errorf("probed video %+v, want 320x180 and about 2000 ms", info)
		}
		waitFor(t, "preview with the frame", func() bool { return strings.Contains(html(), "video:"+fi.Id+":320x180:true") })
		m.feed.ScrollToPost(post.Id)
		shot(t, m, "video-preview")

		// Клик по превью — ролик играет на месте превью, кадры идут.
		m.feed.onAnchor(videoLink + fi.Id)
		v := m.feed.videos[fi.Id]
		if v == nil {
			t.Error("no inline video after a click on the preview")
			return
		}
		playing := func() bool {
			return v.mp.PlaybackState() == multimedia.QMediaPlayer__PlayingState && v.mp.Position() > 0 && v.frame != nil
		}
		if !waitFor(t, "inline video playing", playing) {
			t.Logf("inline status: %q", v.status)
			return
		}
		if v.frame.Width() != 320 || v.frame.Height() != 180 {
			t.Errorf("inline frame %dx%d, want 320x180", v.frame.Width(), v.frame.Height())
		}
		img := v.imageAt(v.pos)
		g := v.w.Geometry()
		if !v.w.IsVisible() || g.Width() != int(img.Width()) || g.Height() != int(img.Height()) {
			t.Errorf("inline video %dx%d visible=%v, preview is %.0fx%.0f", g.Width(), g.Height(), v.w.IsVisible(), img.Width(), img.Height())
		}
		// Независимо от того, как место искали: середина виджета попадает в саму картинку.
		mid := qt.NewQPoint2(g.X()+g.Width()/2, g.Y()+g.Height()/2)
		if hit := m.feed.view.CursorForPosition(mid).Position(); hit != v.pos && hit != v.pos+1 {
			t.Errorf("middle of the inline video hits document position %d, the preview image is at %d", hit, v.pos)
		}
		if got := v.time.Text(); !strings.HasSuffix(got, "/ 0:02") {
			t.Errorf("inline time %q", got)
		}
		shot(t, m, "video-inline")

		// Едет вместе с лентой.
		y := g.Y()
		m.feed.bar.SetValue(m.feed.bar.Value() - 40)
		settle()
		if dy := v.w.Geometry().Y() - y; dy != 40 {
			t.Errorf("inline video moved by %d px when the feed scrolled by 40", dy)
		}

		// Повторный клик — пауза, ещё один — дальше.
		m.feed.onAnchor(videoLink + fi.Id)
		if v.mp.PlaybackState() != multimedia.QMediaPlayer__PausedState {
			t.Errorf("second click did not pause: state %d", v.mp.PlaybackState())
		}

		// «Во весь экран» — отдельное окно с той же позиции; закрыли — ролик
		// продолжается в ленте.
		m.feed.onAnchor(videoLink + fi.Id)
		v.full.Click()
		pl := m.players[fi.Id]
		if pl == nil {
			t.Error("no fullscreen window")
			return
		}
		if !pl.w.IsFullScreen() || v.mp.PlaybackState() == multimedia.QMediaPlayer__PlayingState {
			t.Errorf("fullscreen=%v, inline state %d: want fullscreen window and paused inline", pl.w.IsFullScreen(), v.mp.PlaybackState())
		}
		if got := pl.w.WindowTitle(); got != "bug.avi — carol, # backend" {
			t.Errorf("fullscreen title %q", got)
		}
		if !waitFor(t, "fullscreen playing", func() bool {
			sz := pl.video.VideoSink().VideoSize()
			return pl.mp.PlaybackState() == multimedia.QMediaPlayer__PlayingState && sz.Width() == 320
		}) {
			t.Logf("player message: %q", pl.msg.Text())
			return
		}
		shotWidget(t, pl.w, "video-fullscreen")
		pl.w.Close()
		waitFor(t, "fullscreen closed", func() bool { return m.players[fi.Id] == nil })
		if v.mp.PlaybackState() != multimedia.QMediaPlayer__PlayingState {
			t.Errorf("inline did not continue after fullscreen: state %d", v.mp.PlaybackState())
		}

		// Другой канал — ролик останавливается и убирается.
		m.selectChannel(channelByName(m, "dev", "town-square").Id)
		if len(m.feed.videos) != 0 {
			t.Errorf("%d inline videos left after switching channels", len(m.feed.videos))
		}
	})
}
