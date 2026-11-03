package ui

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Отправка без сети: запрос не дошёл или связи нет — сообщение в очереди и в
// ленте («Не отправлено»), поле ввода работает и чистое; очередь — в снимке;
// связь вернулась — уходит по порядку, без дублей; отказ сервера —
// «Повторить» и «Удалить».
func TestOutbox(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		town := m.current
		c := m.composer
		queuedIn := func(f *Feed) []string {
			var out []string
			for _, it := range f.items {
				if isQueued(it.post) {
					out = append(out, it.post.Message)
				}
			}
			return out
		}
		send := func(text string) {
			c.edit.SetPlainText(text)
			c.send()
		}

		// pending_post_id не повторяется и в одну миллисекунду.
		if a, b := m.newPost(&model.Post{}), m.newPost(&model.Post{}); a.PendingPostId == b.PendingPostId {
			t.Errorf("same pending_post_id %q", a.PendingPostId)
		}

		// Запрос не дошёл: в очереди и в ленте, поле чистое.
		fake.SetUnreachable(true)
		send("первое")
		if !waitFor(t, "queued after a failed request", func() bool { return len(m.outbox.items) == 1 }) {
			return
		}
		if got := queuedIn(m.feed); len(got) != 1 || got[0] != "первое" || c.edit.ToPlainText() != "" {
			t.Errorf("queued in the feed %q, composer %q", got, c.edit.ToPlainText())
		}
		it := m.feed.items[m.feed.index(m.outbox.items[0].post.Id)]
		if !strings.Contains(it.html, "Не отправлено") || !strings.Contains(it.html, outboxDeleteLink) || strings.Contains(it.html, threadLink) {
			t.Errorf("queued post html:\n%s", it.html)
		}

		// Связи нет: поле работает, сообщение — сразу в очередь.
		m.OnDisconnected(errors.New("test: connection reset"), 5*time.Second)
		settle()
		if !c.frame.IsEnabled() || !strings.Contains(c.edit.PlaceholderText(), "Нет связи") {
			t.Errorf("offline composer: enabled %v, placeholder %q", c.frame.IsEnabled(), c.edit.PlaceholderText())
		}
		send("второе")
		send("лишнее")
		if got := queuedIn(m.feed); strings.Join(got, ",") != "первое,второе,лишнее" {
			t.Errorf("queued %q", got)
		}
		shot(t, m, "outbox")
		m.outboxAction(m.outbox.items[2].post.Id, false) // «Удалить»
		if got := queuedIn(m.feed); strings.Join(got, ",") != "первое,второе" || len(m.outbox.items) != 2 {
			t.Errorf("after delete: %q, queue %d", got, len(m.outbox.items))
		}

		// Очередь — в снимке окна.
		m.saveState(true)
		if st, err := readState(m.statePath); err != nil || st == nil || len(st.Outbox) != 2 || st.Outbox[1].Message != "второе" {
			t.Errorf("snapshot outbox: %v %+v", err, st)
		}

		// Связь вернулась — по порядку, без дублей.
		fake.SetUnreachable(false)
		m.OnConnected()
		if !waitFor(t, "queue sent", func() bool { return len(m.outbox.items) == 0 && len(queuedIn(m.feed)) == 0 }) {
			return
		}
		var msgs []string
		for _, it := range m.feed.items {
			if it.post.Message == "первое" || it.post.Message == "второе" || it.post.Message == "лишнее" {
				msgs = append(msgs, it.post.Message)
			}
		}
		if strings.Join(msgs, ",") != "первое,второе" || fake.Last(town).Message != "второе" {
			t.Errorf("after reconnect: feed %q, server last %q", msgs, fake.Last(town).Message)
		}

		// Отказ сервера при отправке очереди — «Повторить».
		m.OnDisconnected(errors.New("test: connection reset"), 5*time.Second)
		settle()
		fake.SetPostPriority(false)
		c.SetPriorities(true)
		c.setPriority(priorityImportant)
		send("важное")
		m.OnConnected()
		if !waitFor(t, "rejected", func() bool { return len(m.outbox.items) == 1 && m.outbox.items[0].err != "" }) {
			for _, q := range m.outbox.items {
				t.Logf("queued %q err %q priority %q sending %v online %v", q.post.Message, q.err, postPriority(q.post), m.outbox.sending, m.online)
			}
			t.Logf("server last %q", fake.Last(town).Message)
			return
		}
		id := m.outbox.items[0].post.Id
		if h := m.feed.items[m.feed.index(id)].html; !strings.Contains(h, outboxRetryLink) {
			t.Errorf("rejected post html:\n%s", h)
		}
		fake.SetPostPriority(true)
		m.outboxAction(id, true)
		if !waitFor(t, "retried", func() bool { return len(m.outbox.items) == 0 && fake.Last(town).Message == "важное" }) {
			return
		}
	})
}
