package ui

import (
	"io"
	"log/slog"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// «Черновики»: строка в сайдбаре с их числом, пока они есть; список — с
// сервера и набранные здесь (канал, тред); «Отправить», «Удалить», «Открыть».
func TestDraftList(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		alice := fake.UserID("alice")
		alerts := ""
		root := openTown(t, m)
		if root == nil {
			return
		}
		town := m.current
		alerts = channelByName(m, "ops", "alerts").Id
		fake.SetDraft(alice, alerts, "", "из веб-клиента")
		m.drafts.reload = true
		m.drafts.sidebarLoaded(true) // как после переподключения: черновики с сервера заново
		if !waitFor(t, "server draft", func() bool { return m.draftCount() == 1 && m.items[draftsID] != nil }) {
			return
		}

		// Набрали в канале и в треде.
		m.composer.edit.SetPlainText("черновик в town")
		m.openThread(root.Id)
		waitFor(t, "thread", func() bool { return m.thread != nil && m.thread.tl.Loaded })
		m.panel.composer.edit.SetPlainText("ответ в треде")
		backend := channelByName(m, "dev", "backend").Id
		m.selectChannel(backend)
		settle()
		if n := m.draftCount(); n != 3 {
			t.Errorf("drafts %d, want 3", n)
		}
		m.selectChannel(draftsID)
		settle()
		items := m.draftList()
		if len(items) != 3 {
			t.Errorf("list %d", len(items))
			return
		}
		shot(t, m, "drafts")

		// Отправить черновик из alerts.
		m.sendDraft(draftKey{channel: alerts})
		if !waitFor(t, "draft sent", func() bool {
			last := fake.Last(alerts)
			return last != nil && last.Message == "из веб-клиента" && m.draftCount() == 2
		}) {
			return
		}
		if !waitFor(t, "server draft deleted", func() bool { return fake.Draft(alice, alerts, "") == "" }) {
			return
		}
		// Удалить черновик town.
		m.deleteDraft(draftKey{channel: town})
		if m.draftCount() != 1 {
			t.Errorf("after delete: %d", m.draftCount())
		}
		// Открыть черновик треда — тред и текст в его поле.
		m.openDraft(draftKey{channel: town, root: root.Id})
		if !waitFor(t, "thread draft opened", func() bool {
			return m.thread != nil && m.thread.rootID == root.Id && m.panel.composer.DraftText(root.Id) == "ответ в треде"
		}) {
			return
		}
		m.deleteDraft(draftKey{channel: town, root: root.Id})
		settle()
		if m.draftCount() != 0 || m.items[draftsID] != nil {
			t.Errorf("drafts left %d, row %v", m.draftCount(), m.items[draftsID] != nil)
		}
	})
}
