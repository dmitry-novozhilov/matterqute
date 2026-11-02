package mmfake

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// Каналы: обзор открытых, вход, создание, выход, настройки уведомлений
// участника. Пользователю — события user_added, user_removed,
// channel_created, channel_member_updated, как у сервера.

// publicChannels — открытые каналы команды (и те, где пользователя нет).
func (s *Server) publicChannels(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*model.Channel{}
	for _, c := range s.channels {
		if c.TeamId == r.PathValue("tid") && c.Type == model.ChannelTypeOpen && c.DeleteAt == 0 {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b *model.Channel) int { return strings.Compare(a.Name, b.Name) })
	s.json(w, out)
}

// joinChannel — POST /channels/{cid}/members: войти в открытый канал (себя).
func (s *Server) joinChannel(w http.ResponseWriter, r *http.Request, me *model.User) {
	var body struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.UserID != me.Id {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.channels[r.PathValue("cid")]
	if c == nil || c.Type != model.ChannelTypeOpen && s.members[me.Id][c.Id] == nil {
		s.fail(w, http.StatusForbidden, "api.context.permissions.app_error")
		return
	}
	if s.members[me.Id][c.Id] == nil {
		s.addChannel(c, []*model.User{me})
		s.memberEvent(model.WebsocketEventUserAdded, me.Id, c)
	}
	w.WriteHeader(http.StatusCreated)
	s.json(w, s.members[me.Id][c.Id])
}

// createChannel — POST /channels: открытый или приватный, создатель — участник.
func (s *Server) createChannel(w http.ResponseWriter, r *http.Request, me *model.User) {
	var c model.Channel
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil || c.Name == "" || c.DisplayName == "" ||
		c.Type != model.ChannelTypeOpen && c.Type != model.ChannelTypePrivate {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.channels {
		if o.TeamId == c.TeamId && o.Name == c.Name {
			s.failMessage(w, http.StatusBadRequest, "store.sql_channel.save_channel.exists.app_error", "A channel with that name already exists on the same team.")
			return
		}
	}
	c.Id, c.CreatorId, c.CreateAt = model.NewId(), me.Id, model.GetMillis()
	s.addChannel(&c, []*model.User{me})
	s.memberEvent(model.WebsocketEventChannelCreated, me.Id, &c)
	w.WriteHeader(http.StatusCreated)
	s.json(w, &c)
}

// leaveChannel — DELETE /channels/{cid}/members/{uid}: выйти (только себя).
func (s *Server) leaveChannel(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.channels[r.PathValue("cid")]
	if c == nil || r.PathValue("uid") != me.Id || s.members[me.Id][c.Id] == nil {
		s.fail(w, http.StatusForbidden, "api.context.permissions.app_error")
		return
	}
	if c.Name == model.DefaultChannelName {
		s.failMessage(w, http.StatusBadRequest, "api.channel.remove.default.app_error", "Cannot remove user from the default channel town-square.")
		return
	}
	delete(s.members[me.Id], c.Id)
	s.memberEvent(model.WebsocketEventUserRemoved, me.Id, c)
	s.json(w, map[string]string{"status": "OK"})
}

// memberNotifyProps — PUT /channels/{cid}/members/{uid}/notify_props.
func (s *Server) memberNotifyProps(w http.ResponseWriter, r *http.Request, me *model.User) {
	var props map[string]string
	if err := json.NewDecoder(r.Body).Decode(&props); err != nil {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.members[me.Id][r.PathValue("cid")]
	if m == nil || r.PathValue("uid") != me.Id {
		s.fail(w, http.StatusForbidden, "api.context.permissions.app_error")
		return
	}
	for k, v := range props {
		m.NotifyProps[k] = v
	}
	data, _ := json.Marshal(m)
	ev := model.NewWebSocketEvent(model.WebsocketEventChannelMemberUpdated, "", "", me.Id, nil, "")
	ev.Add("channelMember", string(data))
	s.toUser(me.Id, ev)
	s.json(w, map[string]string{"status": "OK"})
}

// memberEvent — пользователю userID о канале c (под s.mu).
func (s *Server) memberEvent(kind model.WebsocketEventType, userID string, c *model.Channel) {
	ev := model.NewWebSocketEvent(kind, c.TeamId, "", userID, nil, "")
	ev.Add("user_id", userID)
	ev.Add("channel_id", c.Id)
	s.toUser(userID, ev)
}

// toUser — событие всем соединениям пользователя (под s.mu).
func (s *Server) toUser(userID string, ev *model.WebSocketEvent) {
	data, _ := ev.ToJSON()
	for c := range s.conns {
		if c.userID == userID {
			c.write(data)
		}
	}
}

// channelMember — GET /channels/{cid}/members/{uid}: участник канала.
func (s *Server) channelMember(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.members[r.PathValue("uid")][r.PathValue("cid")]
	if m == nil || s.members[me.Id][r.PathValue("cid")] == nil {
		s.fail(w, http.StatusNotFound, "app.channel.get_member.missing.app_error")
		return
	}
	s.json(w, m)
}

// channelsAutocomplete — GET /teams/{tid}/channels/autocomplete?name=: открытые
// каналы команды (и где нас нет), подходящие по имени или названию, как сервер.
func (s *Server) channelsAutocomplete(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := strings.ToLower(r.URL.Query().Get("name"))
	out := []*model.Channel{}
	for _, c := range s.channels {
		if c.TeamId != r.PathValue("tid") || (c.Type != model.ChannelTypeOpen && s.members[me.Id][c.Id] == nil) {
			continue
		}
		if strings.Contains(strings.ToLower(c.Name), q) || strings.Contains(strings.ToLower(c.DisplayName), q) {
			out = append(out, c)
		}
	}
	s.json(w, out)
}
