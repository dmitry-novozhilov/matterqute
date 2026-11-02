package ui

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// Каналы: обзор и вход, выключить и включить уведомления, создание, выход.
func TestChannels(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		ctx := context.Background()
		me := m.s.Me.Id
		town := m.current
		titles := func(items []menuItem) (out []string) {
			for _, x := range items {
				out = append(out, x.title)
			}
			return out
		}
		dev := m.st.Channel(town).TeamId
		if got := strings.Join(titles(m.sidebarItems(dev, "")), "|"); got != "Обзор каналов…|Создать канал…" {
			t.Errorf("team menu %q", got)
		}
		if got := strings.Join(titles(m.sidebarItems("", town)), "|"); got != "Участники|Добавить участников…|Настройки канала…||В избранное|Переместить в||Выключить уведомления|Уведомления на рабочий стол|Копировать ссылку на канал" {
			t.Errorf("town-square menu %q", got)
		}
		backend := ""
		for _, ch := range m.st.TeamChannels(dev) {
			if ch.Name == "backend" {
				backend = ch.Id
			}
		}
		if got := strings.Join(titles(m.sidebarItems("", backend)), "|"); got != "Участники|Добавить участников…|Настройки канала…||В избранное|Переместить в||Выключить уведомления|Уведомления на рабочий стол|Копировать ссылку на канал||Покинуть канал…" {
			t.Errorf("backend menu %q", got)
		}
		for _, ch := range m.st.DirectChannels() {
			if got := titles(m.sidebarItems("", ch.Id)); strings.Contains(strings.Join(got, "|"), "Покинуть") {
				t.Errorf("DM menu %q", got)
			}
		}

		// Обзор: канала frontend в сайдбаре нет; фильтр, Enter — вошли и открыли.
		m.showBrowseChannels("")
		d := m.channelDialog
		var list *qt.QListWidget
		var filter *qt.QLineEdit
		for _, o := range descendants(d.w.QObject) {
			switch {
			case o.Inherits("QListWidget"):
				list = qt.UnsafeNewQListWidget(o.UnsafePointer())
			case o.Inherits("QLineEdit"):
				filter = qt.UnsafeNewQLineEdit(o.UnsafePointer())
			}
		}
		if !waitFor(t, "public channels", func() bool { return list.Count() >= 3 }) {
			return
		}
		shotWidget(t, d.w.QWidget, "browse-channels")
		filter.SetText("front")
		if list.Count() != 1 || !strings.HasPrefix(list.Item(0).Text(), "frontend") {
			t.Errorf("filtered list: %d, %q", list.Count(), list.Item(0).Text())
			return
		}
		labelled(d.w.QObject, "Войти").Click()
		if !waitFor(t, "joined frontend", func() bool {
			ch := m.st.Channel(m.current)
			return ch != nil && ch.Name == "frontend" && d.closed
		}) {
			return
		}
		frontend := m.current

		// Выключить уведомления — на сервере и в сайдбаре; включить обратно.
		mark := func() string {
			cm, _, err := s.Client.GetChannelMember(ctx, frontend, me, "")
			if err != nil {
				return err.Error()
			}
			return cm.NotifyProps[model.MarkUnreadNotifyProp]
		}
		m.setMuted(frontend, true)
		waitFor(t, "muted", func() bool { return m.st.Muted(frontend) && mark() == model.ChannelMarkUnreadMention })
		if got := titles(m.sidebarItems("", frontend)); !slices.Contains(got, "Включить уведомления") {
			t.Errorf("muted channel menu %q", got)
		}
		m.setMuted(frontend, false)
		waitFor(t, "unmuted", func() bool { return !m.st.Muted(frontend) && mark() == model.ChannelMarkUnreadAll })

		// Создать: адрес — транслитом из названия, приватный.
		m.showCreateChannel("")
		d = m.channelDialog
		var edits []*qt.QLineEdit
		for _, o := range descendants(d.w.QObject) {
			if o.Inherits("QLineEdit") {
				edits = append(edits, qt.UnsafeNewQLineEdit(o.UnsafePointer()))
			}
		}
		edits[0].SetText("Релизы 2.0")
		if edits[1].Text() != "relizy-2-0" {
			t.Errorf("auto address %q", edits[1].Text())
		}
		for _, o := range descendants(d.w.QObject) {
			if o.Inherits("QRadioButton") && strings.HasPrefix(qt.UnsafeNewQRadioButton(o.UnsafePointer()).Text(), "Приватный") {
				qt.UnsafeNewQRadioButton(o.UnsafePointer()).SetChecked(true)
			}
		}
		shotWidget(t, d.w.QWidget, "create-channel")
		labelled(d.w.QObject, "Создать").Click()
		if !waitFor(t, "created and opened", func() bool {
			ch := m.st.Channel(m.current)
			return ch != nil && ch.Name == "relizy-2-0" && ch.Type == model.ChannelTypePrivate && ch.DisplayName == "Релизы 2.0"
		}) {
			return
		}
		created := m.current

		// Тот же адрес второй раз — ошибка сервера в окне, окно не закрылось.
		m.showCreateChannel("")
		d = m.channelDialog
		edits = edits[:0]
		for _, o := range descendants(d.w.QObject) {
			if o.Inherits("QLineEdit") {
				edits = append(edits, qt.UnsafeNewQLineEdit(o.UnsafePointer()))
			}
		}
		edits[0].SetText("Релизы 2.0")
		labelled(d.w.QObject, "Создать").Click()
		waitFor(t, "duplicate refused", func() bool { return !d.closed && labelText(d.w.QObject, "Не создан") != nil })
		d.w.Close()

		// Покинуть: канал пропал, открыт канал по умолчанию.
		m.confirmLeave(created)
		labelled(m.channelDialog.w.QObject, "Покинуть").Click()
		waitFor(t, "left", func() bool { return m.items[created] == nil && m.current == town })
		if m.feeds[created] != nil {
			t.Error("feed of the left channel is still kept")
		}
	})
}
