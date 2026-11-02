package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// Участники: число в шапке, список (администраторы первыми), фильтр,
// добавить из команды, удалить; список следует за открытым каналом.
func TestChannelMembers(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		town := m.current
		backend := m.st.ChannelByName("backend", "").Id
		m.selectChannel(backend)
		if !waitFor(t, "member count in header", func() bool { return m.membersBtn.Text() == "4" }) {
			return
		}
		mp := m.members
		m.toggleMembers()
		if !waitFor(t, "members list", func() bool { return mp.list.Count() == 4 }) {
			return
		}
		names := func() (out []string) {
			for i := range mp.list.Count() {
				out = append(out, m.st.Username(mp.list.Item(i).Data(memberIDRole).ToString()))
			}
			return out
		}
		if got := strings.Join(names(), " "); got != "carol alice bob dave" || !mp.admins[fake.UserID("carol")] {
			t.Errorf("members %q, admins %v", got, mp.admins)
		}
		if mp.info.Text() != "4 участника" {
			t.Errorf("info %q", mp.info.Text())
		}
		shot(t, m, "members")
		mp.filter.SetText("бор")
		if got := strings.Join(names(), " "); got != "bob" || mp.info.Text() != "Найдено: 1 из 4" {
			t.Errorf("filtered %q, info %q", got, mp.info.Text())
		}
		mp.filter.Clear()

		titles := func(items []menuItem) string {
			var out []string
			for _, x := range items {
				out = append(out, x.title)
			}
			return strings.Join(out, "|")
		}
		dave := fake.UserID("dave")
		if got := titles(m.memberItems(backend, dave)); got != "Профиль|Написать||Удалить из канала" {
			t.Errorf("member menu %q", got)
		}
		if got := titles(m.memberItems(backend, m.s.Me.Id)); got != "Профиль|Написать||Покинуть канал…" {
			t.Errorf("own menu %q", got)
		}
		if got := titles(m.memberItems(town, dave)); got != "Профиль|Написать" {
			t.Errorf("town-square menu %q", got)
		}

		// Добавить: в команде, но не в канале, — только erin.
		pp := m.showAddMembers(backend)
		if !waitFor(t, "people not in channel", func() bool { return len(pp.shown) == 1 }) {
			return
		}
		if pp.shown[0].Username != "erin" || pp.list.CurrentRow() != -1 {
			t.Errorf("candidates %v, current %d", pp.shown[0].Username, pp.list.CurrentRow())
		}
		pp.field.SetText("эр")
		if pp.list.CurrentRow() != 0 {
			t.Errorf("typed: current %d", pp.list.CurrentRow())
		}
		pp.pick()
		if len(pp.chosen) != 1 || !pp.picked.IsVisible() || pp.field.Text() != "" || len(pp.shown) != 0 {
			t.Errorf("picked %v, chips %v, field %q, shown %d", pp.ids(), pp.picked.IsVisible(), pp.field.Text(), len(pp.shown))
		}
		shotWidget(t, m.channelDialog.w.QWidget, "add-members")
		labelled(m.channelDialog.w.QObject, "Добавить").Click()
		if !waitFor(t, "erin added", func() bool {
			return m.channelDialog.closed && mp.list.Count() == 5 && m.membersBtn.Text() == "5"
		}) {
			return
		}

		// Удалить dave.
		m.removeMember(backend, dave)
		if !waitFor(t, "dave removed", func() bool {
			return mp.list.Count() == 4 && m.membersBtn.Text() == "4" && !strings.Contains(strings.Join(names(), " "), "dave")
		}) {
			return
		}

		// Список следует за каналом; кнопка закрывает его.
		m.selectChannel(town)
		if !waitFor(t, "town-square members", func() bool { return mp.channel == town && mp.list.Count() == 5 }) {
			return
		}
		if mp.add.IsVisible() != true {
			t.Error("no add button in town-square")
		}
		m.toggleMembers()
		if m.membersShown() {
			t.Error("members still shown")
		}
		for _, ch := range m.st.DirectChannels() {
			if ch.Type == model.ChannelTypeDirect {
				m.selectChannel(ch.Id)
				if m.membersBtn.IsVisible() {
					t.Error("members button in DM")
				}
				break
			}
		}
	})
}

// Настройки канала: назначение и шапка, отказ сервера под полями, у лички —
// только шапка, адрес Town Square не меняется.
func TestChannelSettings(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		town := m.current
		backend := m.st.ChannelByName("backend", "").Id
		m.selectChannel(backend)
		if !m.head.chev.IsVisible() {
			t.Error("no channel menu arrow")
		}
		form := func() (edits []*qt.QLineEdit, head *qt.QPlainTextEdit, hint *qt.QLabel) {
			for _, o := range descendants(m.channelDialog.w.QObject) {
				switch {
				case o.Inherits("QLineEdit"):
					edits = append(edits, qt.UnsafeNewQLineEdit(o.UnsafePointer()))
				case o.Inherits("QPlainTextEdit"):
					head = qt.UnsafeNewQPlainTextEdit(o.UnsafePointer())
				case o.Inherits("QLabel") && qt.UnsafeNewQLabel(o.UnsafePointer()).Property("role").ToString() == "hint":
					hint = qt.UnsafeNewQLabel(o.UnsafePointer())
				}
			}
			return
		}

		m.showChannelSettings(backend)
		edits, head, _ := form()
		if len(edits) != 3 || head == nil || edits[0].Text() != "backend" || edits[2].Text() != "Сервисы и API" {
			t.Errorf("form: %d edits, head %v", len(edits), head != nil)
			return
		}
		shotWidget(t, m.channelDialog.w.QWidget, "channel-settings")
		edits[0].SetText("Бэкенд")
		edits[2].SetText("Новое назначение")
		head.SetPlainText("Дежурный — **bob**")
		labelled(m.channelDialog.w.QObject, "Сохранить").Click()
		if !waitFor(t, "channel patched", func() bool {
			ch := m.st.Channel(backend)
			return m.channelDialog.closed && ch.DisplayName == "Бэкенд" && ch.Header == "Дежурный — **bob**" &&
				strings.Contains(m.head.title.Text(), "Бэкенд") && strings.Contains(m.head.sub.Text(), "Новое назначение")
		}) {
			return
		}

		// Адрес занят — сервер отказывает, окно остаётся с ответом.
		m.showChannelSettings(backend)
		edits, _, hint := form()
		edits[1].SetText("frontend")
		labelled(m.channelDialog.w.QObject, "Сохранить").Click()
		if !waitFor(t, "rename refused", func() bool { return hint.IsVisible() && strings.HasPrefix(hint.Text(), "Не сохранено") }) {
			return
		}
		m.channelDialog.w.Close()

		m.showChannelSettings(town)
		if edits, _, _ := form(); edits[1].IsEnabled() {
			t.Error("town-square address editable")
		}
		m.channelDialog.w.Close()
		for _, ch := range m.st.DirectChannels() {
			if ch.Type == model.ChannelTypeDirect {
				m.showChannelSettings(ch.Id)
				if edits, head, _ := form(); len(edits) != 0 || head == nil {
					t.Errorf("DM form: %d edits", len(edits))
				}
				m.channelDialog.w.Close()
				break
			}
		}
	})
}
