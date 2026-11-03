package ui

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// Архивные каналы: в сайдбаре их нет; ссылка на сообщение открывает канал
// для чтения (строка в сайдбаре, пока открыт; поле ввода неактивно, лента
// без правки и реакций); обзор каналов — вкладка «Архивные», и без членства;
// ссылка на канал; архивировали открытый — он остаётся, только для чтения.
func TestArchivedChannels(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		town := m.current
		ctx := context.Background()
		old := m.st.ChannelByName("old-project", "")
		if old == nil || !m.st.Archived(old.Id) || m.items[old.Id] != nil {
			t.Errorf("old-project: known %v, in the sidebar %v", old != nil, old != nil && m.items[old.Id] != nil)
			return
		}
		list, _, err := s.Client.GetPostsForChannel(ctx, old.Id, 0, 10, "", false, false)
		if err != nil || len(list.Order) == 0 {
			t.Errorf("posts of old-project: %v", err)
			return
		}
		post := list.Order[0]

		// Ссылка на сообщение — канал для чтения, сообщение подсвечено.
		m.handleLink(s.ServerURL + "/dev/pl/" + post)
		if !waitFor(t, "archived post", func() bool { return m.current == old.Id && m.feed.highlight == post }) {
			return
		}
		c := m.composer
		if m.items[old.Id] == nil || !c.archived || c.frame.IsEnabled() || !m.feed.ReadOnly || !strings.Contains(m.head.sub.Text(), "В архиве") {
			t.Errorf("archived view: row %v, composer archived %v enabled %v, feed read-only %v, header %q",
				m.items[old.Id] != nil, c.archived, c.frame.IsEnabled(), m.feed.ReadOnly, m.head.sub.Text())
		}
		if c.edit.PlaceholderText() == "" || !strings.Contains(c.edit.PlaceholderText(), "архиве") {
			t.Errorf("placeholder %q", c.edit.PlaceholderText())
		}
		if i := m.feed.index(post); i < 0 || strings.Contains(m.feed.items[i].html, threadLink+post) {
			t.Error("archived post: not in the feed or with «Ответить»")
		}
		shot(t, m, "archived-channel")

		// Ушли — строки нет, поле снова работает.
		m.selectChannel(town)
		if !waitFor(t, "archived row gone", func() bool { return m.items[old.Id] == nil }) {
			return
		}
		if c.archived || !c.frame.IsEnabled() || m.feed.ReadOnly {
			t.Errorf("after leaving: composer archived %v enabled %v, feed read-only %v", c.archived, c.frame.IsEnabled(), m.feed.ReadOnly)
		}

		// Обзор → «Архивные»: legacy, где нас нет, — открыть для чтения.
		m.showBrowseChannels("")
		d := m.channelDialog
		var lw *qt.QListWidget
		for _, o := range descendants(d.w.QObject) {
			if o.Inherits("QListWidget") {
				lw = qt.UnsafeNewQListWidget(o.UnsafePointer())
			}
		}
		labelled(d.w.QObject, "Архивные").Click()
		if !waitFor(t, "archived list", func() bool { return lw.Count() == 2 }) {
			return
		}
		shotWidget(t, d.w.QWidget, "browse-archived")
		var texts []string
		for i := range lw.Count() {
			texts = append(texts, lw.Item(i).Text())
			if strings.HasPrefix(lw.Item(i).Text(), "legacy") {
				lw.SetCurrentRow(i)
			}
		}
		labelled(d.w.QObject, "Открыть").Click()
		var legacy *model.Channel
		if !waitFor(t, "legacy open", func() bool {
			legacy = m.st.Channel(m.current)
			return legacy != nil && legacy.Name == "legacy" && m.feed.Has(firstPost(m.feed))
		}) {
			t.Errorf("archived list %q", texts)
			return
		}
		if m.st.IsMember(legacy.Id) || m.items[legacy.Id] == nil || !c.archived {
			t.Errorf("legacy: member %v, row %v, composer archived %v", m.st.IsMember(legacy.Id), m.items[legacy.Id] != nil, c.archived)
		}

		// Ссылка на архивный канал.
		m.handleLink(s.ServerURL + "/dev/channels/old-project")
		if !waitFor(t, "channel link", func() bool { return m.current == old.Id }) {
			return
		}
		if !waitFor(t, "legacy row gone", func() bool { return m.items[legacy.Id] == nil }) {
			return
		}

		// Открытый канал архивировали — остаётся на экране, только для чтения.
		backend := channelByName(m, "dev", "backend").Id
		m.selectChannel(backend)
		fake.Archive(backend)
		if !waitFor(t, "backend archived", func() bool {
			return m.st.Archived(backend) && m.current == backend && m.items[backend] != nil && c.archived
		}) {
			return
		}
	})
}

// firstPost — id первого поста ленты или "".
func firstPost(f *Feed) string {
	if len(f.items) == 0 {
		return ""
	}
	return f.items[0].post.Id
}
