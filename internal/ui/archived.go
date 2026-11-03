package ui

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
)

// Архивные каналы (DeleteAt > 0), как в веб-клиенте: в сайдбаре их нет, а
// открытый — есть, пока открыт (store.SetOpenArchived), со значком архива;
// читать можно, писать нельзя: поле ввода неактивно и говорит об этом,
// лента без реакций, правки и удаления (Feed.ReadOnly). Открывают их ссылки
// (сообщение, канал), поиск и «Обзор каналов → Архивные»; открытые архивные
// каналы читаются и без членства.

// applyArchived — открытый канал в архиве или нет: строка в сайдбаре, пока
// открыт, поле ввода и лента — только для чтения. Зовут при открытии канала
// и после перезагрузки сайдбара (канал могли архивировать или вернуть).
func (m *MainWindow) applyArchived() {
	archived := m.st.Archived(m.current)
	if open := m.st.OpenArchived(); open != "" && (open != m.current || !archived) {
		m.st.SetOpenArchived("")
		m.rebuildSidebarSoon() // строка прежнего — прочь (не внутри выбора строки)
	}
	if archived {
		m.st.SetOpenArchived(m.current)
	}
	m.composer.SetArchived(archived)
	if f := m.feeds[m.current]; f != nil {
		f.setReadOnly(archived)
	}
}

// openArchived — архивный канал ch: строка в сайдбаре, then — когда она есть
// (выбрать её, перейти к сообщению).
func (m *MainWindow) openArchived(ch *model.Channel, then func()) {
	if m.st.Channel(ch.Id) == nil {
		m.st.AddChannel(ch, nil) // без членства: только читать
	}
	m.st.SetOpenArchived(ch.Id)
	m.fillSidebar()
	if m.items[ch.Id] == nil {
		m.message("Канал недоступен")
		return
	}
	then()
}

// withArchived — канала id нет в сайдбаре: если он в архиве — открыть его
// для чтения (с сервера, если store его не знает) и then; иначе — сообщить.
func (m *MainWindow) withArchived(id string, then func()) {
	open := func(ch *model.Channel) {
		if ch == nil || ch.DeleteAt == 0 {
			m.message("Канал этого сообщения вам недоступен")
			return
		}
		m.openArchived(ch, then)
	}
	if ch := m.st.Channel(id); ch != nil {
		open(ch)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		ch, _, err := m.s.Client.GetChannel(ctx, id)
		m.ui(func() {
			if err != nil {
				m.log.Warn("load channel", "channel", id, "err", err)
				ch = nil
			}
			open(ch)
		})
	}()
}
