package ui

import (
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

func TestSearchTerms(t *testing.T) {
	got := searchTerms(`отчёт* from:bob in:backend "медленный ответ" -staging after:2026-11-01 nginx`)
	want := []string{"отчёт", "медленный", "ответ", "nginx"}
	if !slices.Equal(got, want) {
		t.Errorf("searchTerms = %q, want %q", got, want)
	}
}

// Поиск: справа, результаты новые сверху с подсветкой, «Перейти» — к
// сообщению; тред из результатов — с возвратом к ним.
func TestSearch(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		ctrlF := false
		for _, a := range m.w.Actions() {
			if a.Shortcut().ToString() == "Ctrl+F" {
				ctrlF = true
			}
		}
		if !ctrlF {
			t.Error("no Ctrl+F")
		}
		sp := m.search
		search := func(q string) {
			sp.field.SetText(q)
			qt.QCoreApplication_SendEvent(sp.field.QObject, qt.NewQKeyEvent(qt.QEvent__KeyPress, int(qt.Key_Return), qt.NoModifier).QEvent)
		}
		m.openSearch()
		if !sp.w.IsVisible() || !sp.field.HasFocus() && sp.field.IsVisible() == false {
			t.Error("search panel not open")
		}

		search("созвон")
		if !waitFor(t, "results", func() bool { return strings.HasPrefix(sp.info.Text(), "Найдено") && len(sp.feed.items) > 0 }) {
			t.Logf("info %q", sp.info.Text())
			return
		}
		for i, it := range sp.feed.items {
			if !strings.Contains(strings.ToLower(it.post.Message), "созвон") {
				t.Errorf("result %q has no «созвон»", it.post.Message)
			}
			if i > 0 && it.post.CreateAt > sp.feed.items[i-1].post.CreateAt {
				t.Error("results are not newest first")
				break
			}
		}
		first := sp.feed.items[0]
		if !strings.Contains(first.html, `>созвон</span>`) || !strings.Contains(first.html, "Перейти") || !strings.Contains(first.html, "Town Square") {
			t.Errorf("result html lacks highlight, place or «Перейти»:\n%s", first.html)
		}
		shot(t, m, "search")

		search("from:bob созвон")
		waitFor(t, "from:bob", func() bool {
			return len(sp.feed.items) > 0 && !slices.ContainsFunc(sp.feed.items, func(it *postItem) bool { return it.post.UserId != m.st.UserByName("bob").Id })
		})

		// «Перейти» — канал, сообщение подсвечено; результаты остаются.
		target := sp.feed.items[len(sp.feed.items)-1].post
		m.selectChannel(channelByName(m, "dev", "backend").Id)
		sp.feed.onAnchor(permalinkLink + target.ChannelId + ":" + target.Id)
		waitFor(t, "jump to the result", func() bool { return m.current == target.ChannelId && m.feed.highlight == target.Id && sp.w.IsVisible() })

		// Ответ в треде — тред из результатов, и обратно к ним.
		search(`"staging то же"`)
		if !waitFor(t, "reply found", func() bool { return len(sp.feed.items) == 1 && sp.feed.items[0].post.RootId == root.Id }) {
			return
		}
		sp.feed.openThread(root.Id)
		if !waitFor(t, "thread instead of search", func() bool { return m.panel.w.IsVisible() && !sp.w.IsVisible() && m.panel.back.IsVisibleTo(m.panel.w) }) {
			return
		}
		m.panel.back.Click()
		if !sp.w.IsVisible() || m.panel.w.IsVisible() || len(sp.feed.items) != 1 {
			t.Errorf("back to results: search %v, thread %v, %d results", sp.w.IsVisible(), m.panel.w.IsVisible(), len(sp.feed.items))
		}

		search("несуществующееслово")
		waitFor(t, "nothing found", func() bool { return strings.Contains(sp.info.Text(), "ничего нет") && len(sp.feed.items) == 0 })

		sp.head.close.Click()
		if sp.w.IsVisible() || sp.query != "" {
			t.Error("search not closed")
		}
	})
}
