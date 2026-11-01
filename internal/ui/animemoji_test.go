package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// Анимированное эмодзи сервера крутится в плашке реакции и в тексте; после
// очистки ленты анимация останавливается.
func TestAnimatedEmoji(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		src := "reaction:" + root.Id + ":partyparrot"
		if !waitFor(t, "animated partyparrot pill", func() bool {
			_, sub := m.feed.animSubs["partyparrot"]
			return sub && m.feed.pills[src].render != nil
		}) {
			return
		}
		mv := m.assets.EmojiMovie("partyparrot")
		if mv == nil || mv.FrameCount() != 6 || mv.State() != qt.QMovie__Running {
			t.Fatalf("partyparrot movie %v: want 6 frames, running", mv != nil)
		}
		// Цвет эмодзи в плашке меняется со временем: кадры доходят до картинки.
		colour := func() string {
			img := m.feed.pills[src].normal.ToImage()
			return img.PixelColor(int(14*img.DevicePixelRatio()), int(12*img.DevicePixelRatio())).Name()
		}
		seen := map[string]bool{colour(): true}
		deadline := time.Now().Add(2 * time.Second)
		for len(seen) < 3 && time.Now().Before(deadline) {
			settleFor(50 * time.Millisecond)
			seen[colour()] = true
		}
		if len(seen) < 3 {
			t.Errorf("pill emoji colours over 2 s: %v — not animating", seen)
		}

		// В тексте — ресурс документа, тоже с анимацией.
		m.selectChannel(channelByName(m, "dev", "backend").Id)
		if !waitFor(t, "inline animated emoji", func() bool {
			for _, it := range m.feed.items {
				if strings.Contains(it.html, `<img src="emoji:partyparrot"`) {
					_, sub := m.feed.animSubs["partyparrot"]
					return sub
				}
			}
			return false
		}) {
			return
		}

		// Лента очищена — подписок нет, QMovie стоит.
		m.feed.Reset("")
		if len(m.feed.animSubs) != 0 || mv.State() == qt.QMovie__Running {
			t.Errorf("after reset: %d subscriptions, movie state %d", len(m.feed.animSubs), mv.State())
		}
	})
}
