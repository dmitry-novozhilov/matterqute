package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// Тред в отдельном окне: открывается там, правая колонка свободна (участники
// не закрывают тред), ответ уходит; кнопка возвращает тред на место открытым;
// закрыли окно — тред закрыт, панель снова в разделителе.
func TestThreadWindow(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		m.openThreadWindow(root.Id)
		if !waitFor(t, "thread in its window", func() bool {
			return m.threadWin != nil && m.panel.feed.Has(root.Id) && strings.Contains(m.threadWin.WindowTitle(), "Town Square")
		}) {
			return
		}
		if m.split.IndexOf(m.panel.w) >= 0 || len(m.rightPanels()) != 3 {
			t.Error("thread panel still in the splitter")
		}
		shotWidget(t, m.threadWin, "thread-window")

		// Справа — участники, тред остаётся.
		m.toggleMembers()
		if !m.membersShown() || m.thread == nil || !m.threadWin.IsVisible() {
			t.Errorf("members closed the detached thread: thread %v", m.thread != nil)
		}
		// Ответ из окна.
		m.sendReply("ответ из окна", nil, "", nil)
		if !waitFor(t, "reply in the detached thread", func() bool {
			last := fake.Last(root.ChannelId)
			return last != nil && last.Message == "ответ из окна" && m.panel.feed.Has(last.Id)
		}) {
			return
		}

		// Вернули на место — тред открыт в правой колонке.
		m.toggleDetach()
		settle()
		if m.threadWin != nil || m.split.IndexOf(m.panel.w) != 2 || !m.panel.w.IsVisible() || m.thread == nil {
			t.Errorf("attach: win %v, index %d, visible %v", m.threadWin != nil, m.split.IndexOf(m.panel.w), m.panel.w.IsVisible())
		}
		// Снова отдельно — и закрыли окно: тред закрыт.
		m.detachThread()
		w := m.threadWin
		w.Close()
		settle()
		if m.thread != nil || m.threadWin != nil || m.split.IndexOf(m.panel.w) != 2 || m.panel.w.IsVisible() {
			t.Errorf("window closed: thread %v, win %v, index %d", m.thread != nil, m.threadWin != nil, m.split.IndexOf(m.panel.w))
		}
	})
}
