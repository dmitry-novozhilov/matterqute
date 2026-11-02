package ui

import (
	"fmt"
	"slices"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// «Печатает…»: свой набор — серверу (user_typing по WebSocket, не чаще
// typingEvery на канал или тред), чужой — строкой под полем ввода канала и
// треда («bob печатает…»), пока идут события, и ещё typingFor после
// последнего; сообщение от пишущего убирает его сразу. Обе стороны
// выключаются отдельно: «Вид → «Печатает…»» (config.json → typing).

const (
	typingEvery = 3 * time.Second // свой набор — серверу не чаще
	typingFor   = 6 * time.Second // чужой «печатает…» — столько после последнего события
)

// SetTyping — показывать ли, кто печатает, и сообщать ли, что печатаю я (все окна).
func SetTyping(show, send bool) {
	style.typingShow, style.typingSend = show, send
	for _, f := range style.listeners {
		f()
	}
}

// OnTypingChosen — настройку поменяли в меню (сохранить).
var OnTypingChosen func(show, send bool)

// typingKey — где печатают: канал и тред (parent пусто — сам канал).
type typingKey struct{ channel, parent string }

// typingState — кто где печатает (до какого времени) и когда сами сообщали.
type typingState struct {
	who   map[typingKey]map[string]time.Time
	sent  map[typingKey]time.Time
	timer *qt.QTimer
}

func (m *MainWindow) initTyping() {
	m.typing = &typingState{who: map[typingKey]map[string]time.Time{}, sent: map[typingKey]time.Time{}}
	m.typing.timer = qt.NewQTimer2(m.w.QObject)
	m.typing.timer.SetInterval(1000)
	m.typing.timer.OnTimeout(func() { m.expireTyping(time.Now()) })
}

// userTyping — набираю в поле канала или треда: серверу, если пора.
func (m *MainWindow) userTyping(channelID, parentID string) {
	if !style.typingSend || channelID == "" || !m.online {
		return
	}
	k := typingKey{channelID, parentID}
	if now := time.Now(); now.Sub(m.typing.sent[k]) >= typingEvery {
		if m.s.Typing(channelID, parentID) {
			m.typing.sent[k] = now
		}
	}
}

// applyTyping — событие typing: userID набирает в канале (в треде — parentID).
func (m *MainWindow) applyTyping(channelID, parentID, userID string) {
	if !style.typingShow || userID == m.s.Me.Id {
		return
	}
	k := typingKey{channelID, parentID}
	if m.typing.who[k] == nil {
		m.typing.who[k] = map[string]time.Time{}
	}
	m.typing.who[k][userID] = time.Now().Add(typingFor)
	m.typing.timer.Start2()
	m.updateTypingNotes()
}

// stopTyping — userID отправил сообщение в канал (тред): уже не печатает.
func (m *MainWindow) stopTyping(p *model.Post) {
	k := typingKey{p.ChannelId, p.RootId}
	if users := m.typing.who[k]; users != nil {
		if _, ok := users[p.UserId]; ok {
			delete(users, p.UserId)
			m.updateTypingNotes()
		}
	}
}

func (m *MainWindow) expireTyping(now time.Time) {
	for k, users := range m.typing.who {
		for u, until := range users {
			if !until.After(now) {
				delete(users, u)
			}
		}
		if len(users) == 0 {
			delete(m.typing.who, k)
		}
	}
	if len(m.typing.who) == 0 {
		m.typing.timer.Stop()
	}
	m.updateTypingNotes()
}

// updateTypingNotes — «… печатает» под полями канала и треда.
func (m *MainWindow) updateTypingNotes() {
	m.composer.SetTypingNote(m.typingText(typingKey{m.current, ""}))
	if t := m.thread; t != nil {
		m.panel.composer.SetTypingNote(m.typingText(typingKey{t.channelID, t.rootID}))
	} else {
		m.panel.composer.SetTypingNote("")
	}
}

// typingText — «bob печатает…», «bob и carol печатают…», «bob, carol и ещё 2 печатают…».
func (m *MainWindow) typingText(k typingKey) string {
	if !style.typingShow {
		return ""
	}
	var names []string
	for u := range m.typing.who[k] {
		names = append(names, m.st.Username(u))
	}
	slices.Sort(names)
	switch n := len(names); {
	case n == 0:
		return ""
	case n == 1:
		return names[0] + " печатает…"
	case n == 2:
		return names[0] + " и " + names[1] + " печатают…"
	default:
		return fmt.Sprintf("%s, %s и ещё %d печатают…", names[0], names[1], n-2)
	}
}
