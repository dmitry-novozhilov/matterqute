package ui

import (
	"context"
	"fmt"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// Подписка на тред при свёрнутых тредах (CRT), как в веб-клиенте: тред
// подписанный — в списке «Треды» и с уведомлениями об ответах. Подписаться
// и отписаться — колокольчиком в шапке панели треда, в меню сообщения и в
// меню списка тредов. Сервер присылает thread_follow_changed (сайдбар
// перечитается), но список меняем сразу, по ответу.

// threadFollowing — подписаны ли на тред rootID; ok=false — свёрнутых тредов нет.
func (m *MainWindow) threadFollowing(rootID string) (following, ok bool) {
	if !m.st.CRT() {
		return false, false
	}
	return m.st.Thread(rootID) != nil, true
}

// followThread — подписаться (on) на тред rootID в канале channelID или отписаться.
func (m *MainWindow) followThread(rootID, channelID string, on bool) {
	team := m.st.TeamFor(channelID)
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		_, err := m.s.Client.UpdateThreadFollowForUser(ctx, m.s.Me.Id, team, rootID, on)
		var th *model.ThreadResponse
		if err == nil && on {
			th, _, _ = m.s.Client.GetUserThread(ctx, m.s.Me.Id, team, rootID, false) // не вышло — придёт с сайдбаром
		}
		m.ui(func() {
			if err != nil {
				m.fail("follow thread", fmt.Errorf("follow=%v thread %s: %w", on, rootID, err))
				return
			}
			if on && th != nil {
				m.st.ApplyThread(th)
				m.withUsers(threadPostAuthors([]*model.ThreadResponse{th}), m.refreshThreads)
			} else if !on {
				m.st.RemoveThread(rootID)
				m.refreshThreads()
			}
			m.updateFollowButton()
		})
	}()
}

// updateFollowButton — колокольчик в шапке панели треда: есть при свёрнутых
// тредах; акцентный — подписаны.
func (m *MainWindow) updateFollowButton() {
	if m.panel == nil || m.panel.follow == nil {
		return // окно ещё строится
	}
	t := m.thread
	b := m.panel.follow
	following, ok := false, false
	if t != nil {
		following, ok = m.threadFollowing(t.rootID)
	}
	b.SetVisible(ok && t.channelID != "")
	if following {
		b.SetIcon(icon("bell", Pal().Acc, 18))
		b.SetToolTip("Вы подписаны на тред — отписаться")
	} else {
		b.SetIcon(icon("bell", Pal().Tx2, 18))
		b.SetToolTip("Подписаться на тред: он будет в «Тредах», с уведомлениями об ответах")
	}
}

// threadsListMenu — правый клик по списку «Треды»: открыть, отписаться.
func (m *MainWindow) threadsListMenu(pos *qt.QPoint) {
	it := m.threadsList.ItemAt(pos)
	if it == nil {
		return
	}
	id := it.Data(threadIDRole).ToString()
	th := m.st.Thread(id)
	if th == nil || th.Post == nil {
		return
	}
	channel := th.Post.ChannelId
	menu := qt.NewQMenu(m.threadsList.QWidget)
	defer menu.DeleteLater()
	fillMenu(menu, []menuItem{
		{title: "Открыть тред", do: func() { m.openThread(id) }},
		{title: "Отписаться от треда", do: func() { m.followThread(id, channel, false) }},
	})
	menu.ExecWithPos(m.threadsList.Viewport().MapToGlobalWithQPoint(pos))
}
