package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// Метки «Важно» и «Срочно»: кнопка — только в поле канала, метка уходит с
// сообщением и сбрасывается, видна плашкой в ленте (сообщение с меткой — со
// своей строкой автора) и в заголовке уведомления; метка черновика
// переживает смену канала; сервер не разрешает — кнопки нет.
func TestPostPriority(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		town := m.current
		c := m.composer
		if c.prioBtn.IsHidden() || !m.panel.composer.prioBtn.IsHidden() {
			t.Errorf("priority button: channel hidden %v, thread hidden %v", c.prioBtn.IsHidden(), m.panel.composer.prioBtn.IsHidden())
		}
		// Кнопка метки — в ряду с остальными (раскладка ряда — TestComposerLayout).
		settle()
		if g, want := c.prioBtn.Geometry(), c.later.Geometry(); g.Y() != want.Y() || g.Height() != want.Height() || g.Width() != want.Width() {
			t.Errorf("priority button %d,%d %dx%d, «later» %d,%d %dx%d", g.X(), g.Y(), g.Width(), g.Height(), want.X(), want.Y(), want.Width(), want.Height())
		}
		pick := func(title string) {
			menu := c.priorityMenu()
			defer menu.DeleteLater()
			for _, a := range menu.Actions() {
				if a.Text() == title {
					a.Trigger()
					return
				}
			}
			t.Errorf("no %q in priority menu", title)
		}
		send := func(text string) *model.Post {
			c.edit.SetPlainText(text)
			c.send()
			var p *model.Post
			waitFor(t, "sent "+text, func() bool {
				for _, it := range m.feed.items {
					if it.post.Message == text {
						p = it.post
					}
				}
				return p != nil
			})
			return p
		}
		pick("Срочное")
		if c.priority != model.PostPriorityUrgent || c.prioNote.IsHidden() || !strings.Contains(c.prioNote.Text(), "СРОЧНО") {
			t.Errorf("urgent chosen: priority %q, note %q hidden %v", c.priority, c.prioNote.Text(), c.prioNote.IsHidden())
		}
		shot(t, m, "priority-composer")
		urgent := send("Прод лежит")
		if urgent == nil {
			return
		}
		if last := fake.Last(town); postPriority(last) != model.PostPriorityUrgent {
			t.Errorf("server got priority %q", postPriority(last))
		}
		if c.priority != "" || !c.prioNote.IsHidden() {
			t.Errorf("priority not reset after send: %q", c.priority)
		}
		pick("Важное")
		important := send("Поднял")
		plain := send("Смотрю логи")
		if important == nil || plain == nil {
			return
		}
		html := func(p *model.Post) string { return m.feed.items[m.feed.index(p.Id)].html }
		if !strings.Contains(html(urgent), "СРОЧНО") || !strings.Contains(html(important), "ВАЖНО") || strings.Contains(html(plain), "ВАЖНО") {
			t.Errorf("badges:\n%s\n%s\n%s", html(urgent), html(important), html(plain))
		}
		if !m.feed.startsGroup(m.feed.index(important.Id)) || m.feed.startsGroup(m.feed.index(plain.Id)) {
			t.Error("a post with a label must start a group, the next plain one continues it")
		}
		shot(t, m, "priority-feed")
		if n := m.notification(urgent); !strings.HasPrefix(n.Title, "Срочно · ") {
			t.Errorf("notification title %q", n.Title)
		}

		// Метка черновика — с ним: ушли из канала и вернулись.
		pick("Важное")
		c.edit.SetPlainText("черновик")
		backend := channelByName(m, "dev", "backend").Id
		m.selectChannel(backend)
		if c.priority != "" {
			t.Errorf("label leaked into another channel: %q", c.priority)
		}
		m.selectChannel(town)
		if c.priority != priorityImportant {
			t.Errorf("draft label after switching back: %q", c.priority)
		}

		// Сервер меток не разрешает — кнопки нет, метка снята.
		c.SetPriorities(false)
		if !c.prioBtn.IsHidden() || c.priority != "" {
			t.Errorf("priorities off: button hidden %v, priority %q", c.prioBtn.IsHidden(), c.priority)
		}
	})
}
