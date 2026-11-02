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

// Ленты каналов готовы заранее: уход и возврат — та же лента на том же
// месте; событие в скрытом канале — в его ленте; новый размер окна —
// и скрытым лентам, без отложенной раскладки до показа.
func TestFeedCache(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		town, townID := m.feed, m.current
		backend := channelByName(m, "dev", "backend")
		if !waitFor(t, "backend drawn in background", func() bool {
			f := m.feeds[backend.Id]
			return f != nil && f.ready && !f.hasWork()
		}) {
			return
		}

		// Town Square: отмотали вверх — запомнили, какой пост у края.
		town.bar.SetValue(town.bar.Maximum() - 400)
		settle()
		if town.stick {
			t.Error("scrolled up but still sticks to the bottom")
			return
		}
		anchor, top := town.topPost()

		m.selectChannel(backend.Id)
		if m.feed != m.feeds[backend.Id] || town.shown || !m.feed.shown {
			t.Error("backend feed is not the one on screen")
			return
		}
		settle()
		if b := m.feed.bar; b.Maximum() == 0 || b.Value() != b.Maximum() || !m.feed.stick {
			t.Errorf("feed drawn in background opened not at the bottom: %d of %d", b.Value(), b.Maximum())
		}

		// Реакция на пост в скрытом канале — в его ленте сразу.
		target := town.items[len(town.items)-1].post
		fake.React(m.st.UserByName("bob").Id, target.Id, "tada", true)
		if !waitFor(t, "reaction in the hidden feed", func() bool {
			i := town.index(target.Id)
			return i >= 0 && strings.Contains(town.items[i].html, reactLink+target.Id+":tada")
		}) {
			return
		}

		// Окно шире: скрытой ленте — тот же размер сразу, не при показе.
		m.w.Resize(m.w.Width()+200, m.w.Height())
		settleFor(500 * time.Millisecond)
		size := m.feedStack.Size()
		if w := town.Widget(); w.Width() != size.Width() || w.TestAttribute(qt.WA_PendingResizeEvent) || town.view.Viewport().TestAttribute(qt.WA_PendingResizeEvent) {
			t.Errorf("hidden feed %dx%d (stack %dx%d), resize still pending", w.Width(), w.Height(), size.Width(), size.Height())
		}
		drained(town)

		// Вернулись — та же лента, тот же пост у края на том же месте.
		m.selectChannel(townID)
		settle()
		if m.feed != town {
			t.Error("town-square opened not from its ready feed")
			return
		}
		if id, got := town.topPost(); id != anchor || abs(got-top) > 1 {
			t.Errorf("back in town-square: top post %s at %d, was %s at %d", id, got, anchor, top)
		}
	})
}
