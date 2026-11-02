package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// TestReplyLink — у сообщения в ленте канала, из которого тред ещё не начат,
// на месте «N ответов» — ссылка «Ответить»: открывает тред, а с первым ответом
// становится «1 ответ».
func TestReplyLink(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		f := m.feed
		var plain *postItem
		for _, it := range f.items {
			reply := strings.Contains(it.html, threadLink+it.post.Id) && strings.Contains(it.html, ">Ответить<")
			switch {
			case it.post.IsSystemMessage() || it.post.ReplyCount > 0:
				if reply {
					t.Errorf("«Ответить» on %q (system %v, %d replies)", it.post.Message, it.post.IsSystemMessage(), it.post.ReplyCount)
				}
			case !reply:
				t.Errorf("no «Ответить» on %q", it.post.Message)
			default:
				plain = it
			}
		}
		if plain == nil {
			t.Error("no post without a thread in town-square")
			return
		}
		shot(t, m, "reply-link")

		f.onAnchor(threadLink + plain.post.Id)
		if !waitFor(t, "thread of the post", func() bool {
			return m.thread != nil && m.thread.rootID == plain.post.Id && m.panel.w.IsVisible() && len(m.panel.feed.items) == 1
		}) {
			return
		}
		if html := m.panel.feed.items[0].html; strings.Contains(html, ">Ответить<") {
			t.Error("«Ответить» in the thread panel")
		}

		fake.Post(&model.Post{ChannelId: plain.post.ChannelId, RootId: plain.post.Id, UserId: fake.UserID("bob"), Message: "первый ответ"})
		waitFor(t, "«1 ответ» instead of «Ответить»", func() bool {
			i := f.index(plain.post.Id)
			return i >= 0 && strings.Contains(f.items[i].html, "1 ответ<") && !strings.Contains(f.items[i].html, ">Ответить<")
		})
	})
}
