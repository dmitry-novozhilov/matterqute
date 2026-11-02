package ui

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// Закрепить и открепить, сохранить и убрать, пометить непрочитанным: метки в
// ленте, списки справа, «непрочитанное» держится, пока не вернулись в канал.
func TestMessageActions(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		town, f := m.current, m.feed
		var target *model.Post
		for _, it := range f.items[len(f.items)-12:] {
			if it.post.UserId != m.s.Me.Id && !it.post.IsSystemMessage() {
				target = it.post
				break
			}
		}
		if target == nil {
			t.Error("no foreign post near the end of town-square")
			return
		}
		html := func() string {
			if i := f.index(target.Id); i >= 0 {
				return f.items[i].html
			}
			return ""
		}

		// Закрепить: метка у поста, он в списке закреплённых канала; открепить — нет.
		m.pinPost(target, true)
		if !waitFor(t, "pinned mark", func() bool { return strings.Contains(html(), "Закреплено") }) {
			return
		}
		m.toggleList(listPinned)
		if !waitFor(t, "pinned list", func() bool { return m.listShown(listPinned) && m.search.feed.Has(target.Id) }) {
			return
		}
		shot(t, m, "pinned-list")
		m.pinPost(target, false)
		waitFor(t, "unpinned", func() bool { return !strings.Contains(html(), "Закреплено") && !m.search.feed.Has(target.Id) })

		// Сохранить: метка и список сохранённых (настройка на сервере); убрать — нет.
		m.savePost(target, true)
		if !waitFor(t, "saved mark", func() bool { return m.st.Saved(target.Id) && strings.Contains(html(), "Сохранено") }) {
			return
		}
		m.toggleList(listSaved)
		if !waitFor(t, "saved list", func() bool { return m.listShown(listSaved) && m.search.feed.Has(target.Id) }) {
			return
		}
		m.savePost(target, false)
		waitFor(t, "unsaved", func() bool {
			return !m.st.Saved(target.Id) && !strings.Contains(html(), "Сохранено") && !m.search.feed.Has(target.Id)
		})
		m.toggleList(listSaved)
		if m.search.w.IsVisible() {
			t.Error("second click on «saved» did not close the list")
		}

		// Пометить непрочитанным: канал непрочитан, «Новые сообщения» над постом.
		m.markUnread(f, target)
		unread := func() bool { u, _ := m.st.Unread(town); return u }
		if !waitFor(t, "channel unread from the post", func() bool { return unread() && f.newMarker == target.Id }) {
			return
		}
		// Вернулись в окно — не прочитан; ушли в другой канал — и на сервере не прочитан.
		qt.QCoreApplication_SendEvent(m.w.QObject, qt.NewQEvent(qt.QEvent__WindowActivate))
		backend := channelByName(m, "dev", "backend")
		m.selectChannel(backend.Id)
		settleFor(300 * time.Millisecond)
		member, _, err := m.s.Client.GetChannelMember(context.Background(), town, m.s.Me.Id, "")
		if err != nil || member.LastViewedAt >= target.CreateAt || !unread() {
			t.Errorf("leaving a manually unread channel marked it read (server last viewed %v, err %v, unread here %v)", member, err, unread())
		}
		// Вернулись — прочитан.
		m.selectChannel(town)
		waitFor(t, "read after coming back", func() bool { return !unread() })
	})
}
