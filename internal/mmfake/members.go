package mmfake

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// Участники канала: список, добавить, удалить, статистика; правка канала;
// поиск пользователей; групповая личка. События — как у сервера: user_added
// и user_removed участникам канала, channel_updated, group_added.

// paged — страница page (с 0) по per_page (по умолчанию 60) из items.
func paged[T any](items []T, r *http.Request) []T {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	per, err := strconv.Atoi(r.URL.Query().Get("per_page"))
	if err != nil || per <= 0 {
		per = 60
	}
	from := min(page*per, len(items))
	return items[from:min(from+per, len(items))]
}

// sortedUsers — все пользователи по имени (под s.mu).
func (s *Server) sortedUsers() []*model.User {
	out := make([]*model.User, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, u)
	}
	slices.SortFunc(out, func(a, b *model.User) int { return strings.Compare(a.Username, b.Username) })
	return out
}

// inTeam — состоит ли пользователь в команде: участник хоть одного её канала.
func (s *Server) inTeam(userID, teamID string) bool {
	if teamID == "" {
		return true
	}
	for cid := range s.members[userID] {
		if c := s.channels[cid]; c != nil && c.TeamId == teamID {
			return true
		}
	}
	return false
}

// channelMembers — GET /channels/{cid}/members?page&per_page.
func (s *Server) channelMembers(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cid := r.PathValue("cid")
	if s.members[me.Id][cid] == nil {
		s.fail(w, http.StatusForbidden, "api.context.permissions.app_error")
		return
	}
	out := model.ChannelMembers{}
	for _, u := range s.sortedUsers() {
		if m := s.members[u.Id][cid]; m != nil {
			out = append(out, *m)
		}
	}
	s.json(w, paged(out, r))
}

// channelStats — GET /channels/{cid}/stats: число участников.
func (s *Server) channelStats(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cid := r.PathValue("cid")
	if s.members[me.Id][cid] == nil {
		s.fail(w, http.StatusForbidden, "api.context.permissions.app_error")
		return
	}
	n := 0
	for _, chans := range s.members {
		if chans[cid] != nil {
			n++
		}
	}
	s.json(w, &model.ChannelStats{ChannelId: cid, MemberCount: int64(n)})
}

// usersList — GET /users: in_channel — участники канала, not_in_channel (с
// in_team) — кто из команды ещё не в канале.
func (s *Server) usersList(w http.ResponseWriter, r *http.Request, me *model.User) {
	q := r.URL.Query()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*model.User{}
	for _, u := range s.sortedUsers() {
		if in := q.Get("in_channel"); in != "" && s.members[u.Id][in] == nil {
			continue
		}
		if not := q.Get("not_in_channel"); not != "" && (s.members[u.Id][not] != nil || !s.inTeam(u.Id, q.Get("in_team"))) {
			continue
		}
		out = append(out, u)
	}
	s.json(w, paged(out, r))
}

// searchUsers — POST /users/search: по началу имени пользователя, имени или
// фамилии; команда, в канале и не в канале — как у сервера.
func (s *Server) searchUsers(w http.ResponseWriter, r *http.Request, me *model.User) {
	var q model.UserSearch
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	term := strings.ToLower(strings.TrimPrefix(q.Term, "@"))
	if q.Limit <= 0 {
		q.Limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*model.User{}
	for _, u := range s.sortedUsers() {
		if !strings.HasPrefix(u.Username, term) && !strings.HasPrefix(strings.ToLower(u.FirstName), term) &&
			!strings.HasPrefix(strings.ToLower(u.LastName), term) {
			continue
		}
		if !s.inTeam(u.Id, q.TeamId) || q.InChannelId != "" && s.members[u.Id][q.InChannelId] == nil ||
			q.NotInChannelId != "" && s.members[u.Id][q.NotInChannelId] != nil {
			continue
		}
		if out = append(out, u); len(out) == q.Limit {
			break
		}
	}
	s.json(w, out)
}

// addMembers — POST /channels/{cid}/members с user_ids: добавить других (сам
// добавляющий — участник). Участникам — user_added.
func (s *Server) addMembers(w http.ResponseWriter, me *model.User, cid string, ids []string) {
	c := s.channels[cid]
	if c == nil || c.IsGroupOrDirect() || s.members[me.Id][cid] == nil {
		s.fail(w, http.StatusForbidden, "api.context.permissions.app_error")
		return
	}
	out := []*model.ChannelMember{}
	for _, id := range ids {
		u := s.users[id]
		if u == nil {
			s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
			return
		}
		if s.members[id][cid] == nil {
			s.addChannel(c, []*model.User{u})
			ev := model.NewWebSocketEvent(model.WebsocketEventUserAdded, "", cid, "", nil, "")
			ev.Add("user_id", id)
			ev.Add("team_id", c.TeamId)
			s.sendToChannel(cid, ev)
		}
		out = append(out, s.members[id][cid])
	}
	w.WriteHeader(http.StatusCreated)
	s.json(w, out)
}

// removeMember — DELETE /channels/{cid}/members/{uid} другого: участникам
// канала и удалённому — user_removed.
func (s *Server) removeMember(w http.ResponseWriter, me *model.User, c *model.Channel, uid string) {
	if c.IsGroupOrDirect() || s.members[me.Id][c.Id] == nil || s.members[uid][c.Id] == nil {
		s.fail(w, http.StatusForbidden, "api.context.permissions.app_error")
		return
	}
	delete(s.members[uid], c.Id)
	ev := model.NewWebSocketEvent(model.WebsocketEventUserRemoved, "", c.Id, "", nil, "")
	ev.Add("user_id", uid)
	ev.Add("remover_id", me.Id)
	s.sendToChannel(c.Id, ev)
	s.memberEvent(model.WebsocketEventUserRemoved, uid, c)
	s.json(w, map[string]string{"status": "OK"})
}

// patchChannel — PUT /channels/{cid}/patch: у личек — только шапка.
func (s *Server) patchChannel(w http.ResponseWriter, r *http.Request, me *model.User) {
	var patch model.ChannelPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.channels[r.PathValue("cid")]
	if c == nil || s.members[me.Id][c.Id] == nil ||
		c.IsGroupOrDirect() && (patch.Name != nil || patch.DisplayName != nil || patch.Purpose != nil) {
		s.fail(w, http.StatusForbidden, "api.context.permissions.app_error")
		return
	}
	if patch.Name != nil && *patch.Name != c.Name {
		if c.Name == model.DefaultChannelName {
			s.failMessage(w, http.StatusBadRequest, "api.channel.update_channel.tried.app_error", "Tried to perform an invalid update of the default channel town-square.")
			return
		}
		for _, o := range s.channels {
			if o.TeamId == c.TeamId && o.Name == *patch.Name {
				s.failMessage(w, http.StatusBadRequest, "store.sql_channel.update.exists.app_error", "A channel with that name already exists on the same team.")
				return
			}
		}
	}
	c.Patch(&patch)
	c.UpdateAt = model.GetMillis()
	data, _ := json.Marshal(c)
	ev := model.NewWebSocketEvent(model.WebsocketEventChannelUpdated, "", c.Id, "", nil, "")
	ev.Add("channel", string(data))
	s.sendToChannel(c.Id, ev)
	s.json(w, c)
}

// createGroup — POST /channels/group: групповая личка (3–8 человек вместе с
// создателем); такая уже есть — она же.
func (s *Server) createGroup(w http.ResponseWriter, r *http.Request, me *model.User) {
	var ids []string
	if err := json.NewDecoder(r.Body).Decode(&ids); err != nil {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	if !slices.Contains(ids, me.Id) {
		ids = append(ids, me.Id)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) < model.ChannelGroupMinUsers || len(ids) > model.ChannelGroupMaxUsers {
		s.failMessage(w, http.StatusBadRequest, "api.channel.create_group.bad_size.app_error", "Group message channels must contain at least 3 and no more than 8 users.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	name := model.GetGroupNameFromUserIds(ids)
	for _, c := range s.channels {
		if c.Name == name {
			s.json(w, c)
			return
		}
	}
	var users []*model.User
	var names []string
	for _, id := range ids {
		u := s.users[id]
		if u == nil {
			s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
			return
		}
		users = append(users, u)
		names = append(names, u.Username)
	}
	slices.Sort(names)
	c := &model.Channel{Id: model.NewId(), Type: model.ChannelTypeGroup, Name: name, DisplayName: strings.Join(names, ", "), CreatorId: me.Id, CreateAt: model.GetMillis()}
	s.addChannel(c, users)
	for _, u := range users {
		ev := model.NewWebSocketEvent(model.WebsocketEventGroupAdded, "", c.Id, u.Id, nil, "")
		ev.Add("teammate_ids", "[]")
		s.toUser(u.Id, ev)
	}
	w.WriteHeader(http.StatusCreated)
	s.json(w, c)
}
