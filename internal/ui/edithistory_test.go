package ui

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// История правок: у своего изменённого — «(изменено)» ссылкой; окно —
// нынешний текст и прежние версии; прежнюю можно восстановить.
func TestEditHistory(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		ctx := context.Background()
		p, _, err := s.Client.CreatePost(ctx, &model.Post{ChannelId: m.current, Message: "первый вариант"})
		if err != nil {
			t.Error(err)
			return
		}
		for _, text := range []string{"второй вариант", "третий вариант"} {
			if _, _, err := s.Client.PatchPost(ctx, p.Id, &model.PostPatch{Message: &text}); err != nil {
				t.Error(err)
				return
			}
		}
		var it *postItem
		if !waitFor(t, "edited post in feed", func() bool {
			if i := m.feed.index(p.Id); i >= 0 {
				it = m.feed.items[i]
				return it.post.Message == "третий вариант"
			}
			return false
		}) {
			return
		}
		if !strings.Contains(it.html, historyLink+p.Id) {
			t.Error("own edited post: «(изменено)» is not a link")
		}
		v := m.showEditHistory(it.post)
		if !waitFor(t, "history", func() bool { return len(v.versions) == 2 }) {
			return
		}
		text := v.text.ToPlainText()
		if !strings.Contains(text, "третий вариант") || strings.Index(text, "второй вариант") > strings.Index(text, "первый вариант") {
			t.Errorf("history text:\n%s", text)
		}
		shotWidget(t, v.d.w.QWidget, "edit-history")
		v.restore(1) // самая первая версия
		if !waitFor(t, "restored", func() bool {
			last := fake.Last(m.current)
			return v.d.closed && last != nil && last.Id == p.Id && last.Message == "первый вариант"
		}) {
			return
		}
		// У чужих изменённых — просто текст: историю сервер показывает автору.
		for _, x := range m.feed.items {
			if x.post.UserId != m.s.Me.Id && strings.Contains(x.html, historyLink) {
				t.Error("history link on someone else's post")
			}
		}
	})
}
