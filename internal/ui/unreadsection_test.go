package ui

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/mm"
	"matterqute/internal/store"
)

// Непрочитанные отдельной группой: настройка на сервере; открытый канал
// держится в группе, пока открыт; новое сообщение переносит канал в группу.
func TestUnreadSection(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		town := m.current
		backend := channelByName(m, "dev", "backend").Id
		group := func() []string {
			for i := range m.sidebar.TopLevelItemCount() {
				g := m.sidebar.TopLevelItem(i)
				if g.Data(0, groupRole).ToString() == store.UnreadTitle {
					var out []string
					for j := range g.ChildCount() {
						out = append(out, g.Child(j).Data(0, channelIDRole).ToString())
					}
					return out
				}
			}
			return nil
		}
		m.setUnreadSection(true)
		if !slices.Contains(group(), backend) || m.sidebar.TopLevelItem(1).Data(0, groupRole).ToString() != store.UnreadTitle {
			t.Errorf("unread group %v", group())
		}
		if !waitFor(t, "setting on server", func() bool {
			prefs, _, err := s.Client.GetPreferencesByCategory(context.Background(), m.s.Me.Id, mm.PrefSidebarSettings)
			return err == nil && slices.ContainsFunc(prefs, func(p model.Preference) bool { return p.Name == mm.PrefUnreadSection && p.Value == "true" })
		}) {
			return
		}
		shot(t, m, "unread-section")

		// Открыли backend — прочитан, но в группе, пока открыт; ушли — уехал.
		m.selectChannel(backend)
		settle()
		if !slices.Contains(group(), backend) {
			t.Errorf("opened unread channel left the group: %v", group())
		}
		m.selectChannel(town)
		settle()
		if slices.Contains(group(), backend) {
			t.Errorf("read channel still in the group: %v", group())
		}
		// Новое в backend — снова в группе.
		fake.Post(&model.Post{ChannelId: backend, UserId: fake.UserID("bob"), Message: "ещё новости"})
		if !waitFor(t, "backend back in the group", func() bool { return slices.Contains(group(), backend) }) {
			return
		}
		// Выключили из меню группы.
		for i := range m.sidebar.TopLevelItemCount() {
			if g := m.sidebar.TopLevelItem(i); g.Data(0, groupRole).ToString() == store.UnreadTitle {
				items := m.unreadGroupItems(g)
				items[len(items)-1].do()
				break
			}
		}
		if group() != nil || m.st.UnreadSection() {
			t.Error("group still shown after turning off")
		}
	})
}
