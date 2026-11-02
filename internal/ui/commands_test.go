package ui

import (
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/markdown"
)

func TestSlashCommands(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		c := m.composer
		c.edit.SetFocus()
		enter := func(c *Composer) {
			qt.QCoreApplication_SendEvent(c.edit.QObject, qt.NewQKeyEvent(qt.QEvent__KeyPress, int(qt.Key_Return), qt.NoModifier).QEvent)
		}
		titles := func() []string {
			if c.popup == nil || !c.popup.visible() {
				return nil
			}
			var out []string
			for _, it := range c.popup.items {
				out = append(out, it.Title)
			}
			return out
		}
		text := func(c *Composer) string { return markdown.FromBlocks(readBlocks(c.edit.Document())) }
		last := func(f *Feed) *model.Post { return f.items[len(f.items)-1].post }

		// Подсказки команд — с сервера; Enter берёт подсказку.
		c.edit.InsertPlainText("/aw")
		if !waitFor(t, "command suggestions", func() bool { return slices.Contains(titles(), "/away") }) {
			return
		}
		shotWidget(t, m.w, "command-suggest")
		enter(c)
		if got := text(c); got != "/away " {
			t.Errorf("after picking the suggestion: %q", got)
		}

		// /away — статус на сервере и ответ «только вам» в ленте.
		enter(c)
		if !c.empty() {
			t.Errorf("composer keeps %q after the command", text(c))
		}
		if !waitFor(t, "away and ephemeral answer", func() bool {
			p := last(m.feed)
			return m.st.Status(m.s.Me.Id) == model.StatusAway && p.Type == model.PostTypeEphemeral && p.Message == "Вы отошли."
		}) {
			return
		}
		if html := m.feed.items[len(m.feed.items)-1].html; !strings.Contains(html, "Видно только вам") {
			t.Error("ephemeral answer is not marked as visible only to you")
		}

		// /echo с оформлением — обычное сообщение от себя.
		c.setBlocks(markdown.ToBlocks("/echo **жирно**"))
		enter(c)
		waitFor(t, "echo", func() bool { p := last(m.feed); return p.UserId == m.s.Me.Id && p.Message == "**жирно**" })

		// Неизвестная команда: в поле, подсказка; Enter ещё раз — сообщением.
		c.setBlocks(markdown.ToBlocks("/home/alice/notes.txt"))
		enter(c)
		if !waitFor(t, "unknown command offered as text", func() bool {
			return text(c) == "/home/alice/notes.txt" && c.hint.IsVisible() && strings.Contains(c.hint.Text(), "на сервере нет")
		}) {
			return
		}
		shotWidget(t, m.w, "command-unknown")
		enter(c)
		waitFor(t, "sent as text", func() bool { return last(m.feed).Message == "/home/alice/notes.txt" })
		if c.hint.IsVisible() {
			t.Error("hint stays after sending")
		}

		// Ошибка команды — текстом сервера под полем, команда — снова в поле.
		c.setBlocks(markdown.ToBlocks("/join ~nowhere"))
		enter(c)
		waitFor(t, "join error", func() bool {
			return text(c) == "/join ~nowhere" && strings.Contains(c.hint.Text(), "Канала «nowhere» нет")
		})

		// Подсказки аргументов и /join — переход в новый канал.
		c.setBlocks(nil)
		c.edit.InsertPlainText("/join ~fro")
		waitFor(t, "channel suggestions", func() bool { return slices.Contains(titles(), "frontend") })
		enter(c)
		if got := text(c); got != "/join frontend " {
			t.Errorf("join suggestion inserted %q", got)
		}
		enter(c)
		if !waitFor(t, "joined frontend", func() bool { ch := m.st.Channel(m.current); return ch != nil && ch.Name == "frontend" }) {
			return
		}

		// В треде — ответ в треде.
		m.openThread(root.Id)
		if !waitFor(t, "thread", func() bool { return len(m.panel.feed.items) == 4 }) {
			return
		}
		pc := m.panel.composer
		pc.setBlocks(markdown.ToBlocks("/me машет рукой"))
		enter(pc)
		waitFor(t, "command reply in thread", func() bool {
			p := last(m.panel.feed)
			return p.RootId == root.Id && p.Message == "*машет рукой*"
		})
	})
}
