package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"

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

		// Плеер: заголовок, играет, время, пауза, закрытие.
		m.feed.onAnchor(videoLink + fi.Id)
		pl := m.players[fi.Id]
		if pl == nil {
			t.Error("no player window")
			return
		}
		if got := pl.w.WindowTitle(); got != "bug.avi — carol, # backend" {
			t.Errorf("player title %q", got)
		}
		if !waitFor(t, "playing", func() bool {
			return pl.mp.PlaybackState() == multimedia.QMediaPlayer__PlayingState && pl.mp.Position() > 0
		}) {
			t.Logf("player message: %q", pl.msg.Text())
			return
		}
		// Кадры доходят до виджета (сам кадр offscreen на скриншот не попадает).
		if !waitFor(t, "frames in the video widget", func() bool {
			sz := pl.video.VideoSink().VideoSize()
			return sz.Width() == 320 && sz.Height() == 180
		}) {
			return
		}
		if got := pl.time.Text(); !strings.HasSuffix(got, "/ 0:02") {
			t.Errorf("time label %q", got)
		}
		shotWidget(t, pl.w, "video-player")
		pl.play.Click()
		if pl.mp.PlaybackState() != multimedia.QMediaPlayer__PausedState {
			t.Errorf("play button did not pause: state %d", pl.mp.PlaybackState())
		}
		m.feed.onAnchor(videoLink + fi.Id)
		if m.players[fi.Id] != pl {
			t.Error("second click opened another player instead of raising the first")
		}
		pl.w.Close()
		waitFor(t, "player gone", func() bool { return m.players[fi.Id] == nil })
	})
}
