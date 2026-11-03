package mmfake

import (
	"github.com/mattermost/mattermost/server/public/model"
)

// TypingEvent — кто где набирал (что прислали клиенты).
type TypingEvent struct{ UserID, ChannelID, ParentID string }

// typing — пользователь набирает текст: остальным в канале — событие typing.
func (s *Server) typing(userID, channelID, parentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.members[userID][channelID] == nil {
		return
	}
	s.typed = append(s.typed, TypingEvent{userID, channelID, parentID})
	ev := model.NewWebSocketEvent(model.WebsocketEventTyping, "", channelID, "", map[string]bool{userID: true}, "")
	ev.Add("user_id", userID)
	ev.Add("parent_id", parentID)
	data, _ := ev.ToJSON()
	for _, c := range s.connsIn(channelID) {
		if c.userID != userID {
			c.write(data)
		}
	}
}

// Typing — пользователь userID набирает текст в канале (в треде — parentID):
// для разработки и тестов.
func (s *Server) Typing(userID, channelID, parentID string) { s.typing(userID, channelID, parentID) }

// Typed — что набирали клиенты (user_typing), по порядку.
func (s *Server) Typed() []TypingEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]TypingEvent(nil), s.typed...)
}

// activeStatus — клиент сообщил, за компьютером ли пользователь: как
// сервер, «В сети» ↔ «Отошёл» меняем, только если статус не выбран руками
// (здесь — если «отошёл» поставил простой); всем — status_change.
func (s *Server) activeStatus(userID string, active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activity = append(s.activity, active)
	if s.autoAway == nil {
		s.autoAway = map[string]bool{}
	}
	cur := s.statuses[userID]
	next := cur
	switch {
	case !active && cur == model.StatusOnline:
		next = model.StatusAway
		s.autoAway[userID] = true
	case active && cur == model.StatusAway && s.autoAway[userID]:
		next = model.StatusOnline
		delete(s.autoAway, userID)
	}
	if next == cur {
		return
	}
	s.statuses[userID] = next
	ev := model.NewWebSocketEvent(model.WebsocketEventStatusChange, "", "", "", nil, "")
	ev.Add("user_id", userID)
	ev.Add("status", next)
	s.broadcast(ev)
}

// Activity — что клиенты сообщали о простое (true — за компьютером), по порядку.
func (s *Server) Activity() []bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]bool(nil), s.activity...)
}
