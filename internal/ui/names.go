package ui

import (
	"context"
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
)

// Как показывать людей — «Вид → Имена людей» (настройка сервера
// display_settings/name_format, общая с веб-клиентом): имя пользователя,
// псевдоним (нет — имя и фамилия) или имя и фамилия; чего нет — имя
// пользователя (store.Name). Так — авторы в ленте, лички в сайдбаре и
// быстром переходе, «печатает…», уведомления, треды; @упоминания и ссылки
// остаются именами пользователей. Сервер может запретить менять
// (LockTeammateNameDisplay) — тогда пункты серые.

var nameFormats = []struct{ format, title string }{
	{model.ShowUsername, "Имя пользователя"},
	{model.ShowNicknameFullName, "Псевдоним, если есть, иначе имя и фамилия"},
	{model.ShowFullName, "Имя и фамилия"},
}

// setNameFormat — выбрали в меню: сразу здесь, на сервер — настройкой.
func (m *MainWindow) setNameFormat(format string) {
	if cur, locked := m.st.NameFormat(); locked || cur == format {
		return
	}
	m.st.SetNameFormat(format)
	m.namesChanged()
	pref := model.Preference{UserId: m.s.Me.Id, Category: model.PreferenceCategoryDisplaySettings, Name: model.PreferenceNameNameFormat, Value: format}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		_, err := m.s.Client.UpdatePreferences(ctx, m.s.Me.Id, model.Preferences{pref})
		m.ui(func() {
			if err != nil {
				m.fail("save name format", fmt.Errorf("save %s/%s=%s: %w", pref.Category, pref.Name, format, err))
			}
		})
	}()
}

// namesChanged — имена людей показываются иначе: перерисовать, где они есть.
func (m *MainWindow) namesChanged() {
	m.fillSidebar()
	m.eachFeed((*Feed).invalidateAll)
	m.panel.feed.renderAll()
	if m.threadsList != nil {
		m.threadsList.Viewport().Update()
	}
}
