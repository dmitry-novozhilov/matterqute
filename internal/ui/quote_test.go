package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// «Цитировать»: всё сообщение — строками цитаты с оформлением в конец поля,
// курсор под ней; выделенное в сообщении — только оно; из треда — в поле
// треда; в результатах поиска пункта нет.
func TestQuote(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		town := m.current
		p := fake.Post(&model.Post{ChannelId: town, UserId: fake.UserID("bob"), Message: "**жирно** и `код`\nвторая строка"})
		if !waitFor(t, "bob's post", func() bool { return m.feed.Has(p.Id) }) {
			return
		}
		c := m.composer
		send := func(answer string) string {
			cur := c.edit.TextCursor()
			cur.InsertText(answer)
			c.edit.SetTextCursor(cur)
			c.send()
			var got string
			waitFor(t, "sent "+answer, func() bool {
				if last := fake.Last(town); last != nil && strings.HasSuffix(last.Message, answer) {
					got = last.Message
					return true
				}
				return false
			})
			return got
		}

		m.quote(m.feed, p, "")
		shot(t, m, "quote")
		if got, want := send("согласен"), "> **жирно** и `код`\n> вторая строка\n\nсогласен"; got != want {
			t.Errorf("quoted message:\n got %q\nwant %q", got, want)
		}

		// Выделенное — только оно, после уже набранного.
		cur := m.feed.view.Document().Find("вторая")
		m.feed.view.SetTextCursor(cur)
		sel := m.feed.selectionIn(m.feed.items[m.feed.index(p.Id)])
		if sel != "вторая" {
			t.Errorf("selection in the post %q", sel)
		}
		if other := m.feed.selectionIn(m.feed.items[m.feed.index(root.Id)]); other != "" {
			t.Errorf("selection reported for another post: %q", other)
		}
		c.edit.SetPlainText("сначала")
		m.quote(m.feed, p, sel)
		if got := send("потом"); got != "сначала\n> вторая\n\nпотом" && got != "сначала\n\n> вторая\n\nпотом" {
			t.Errorf("quoted selection: %q", got)
		}

		// Из треда — в поле треда.
		m.openThread(root.Id)
		if !waitFor(t, "thread", func() bool { return m.panel.feed.Has(root.Id) }) {
			return
		}
		m.quote(m.panel.feed, root, "")
		if !strings.HasPrefix(m.panel.composer.edit.ToPlainText(), firstLine(root.Message, 1000)) || c.edit.ToPlainText() != "" {
			t.Errorf("thread quote: thread %q, channel %q", m.panel.composer.edit.ToPlainText(), c.edit.ToPlainText())
		}
		if m.search.feed.OnQuote != nil {
			t.Error("«Цитировать» in search results")
		}
	})
}
