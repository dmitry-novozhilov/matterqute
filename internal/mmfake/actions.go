package mmfake

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/mattermost/mattermost/server/public/model"
)

// Действия с сообщением: закрепить и открепить (post_edited всем в канале),
// закреплённые канала, сохранённые (настройки flagged_post), пометить
// непрочитанным — канал (post_unread) и тред (thread_read_changed).

// pin — POST /posts/{pid}/pin и /unpin.
func (s *Server) pin(on bool) func(http.ResponseWriter, *http.Request, *model.User) {
	return func(w http.ResponseWriter, r *http.Request, me *model.User) {
		s.mu.Lock()
		defer s.mu.Unlock()
		p := s.post(r.PathValue("pid"))
		if p == nil || s.members[me.Id][p.ChannelId] == nil {
			s.fail(w, http.StatusNotFound, "app.post.get.app_error")
			return
		}
		p.IsPinned, p.UpdateAt = on, model.GetMillis()
		postJSON, _ := json.Marshal(p)
		ev := model.NewWebSocketEvent(model.WebsocketEventPostEdited, "", p.ChannelId, "", nil, "")
		ev.Add("post", string(postJSON))
		s.sendToChannel(p.ChannelId, ev)
		s.json(w, map[string]string{"status": "OK"})
	}
}

// pinned — GET /channels/{cid}/pinned: закреплённые, новые первыми.
func (s *Server) pinned(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cid := r.PathValue("cid")
	if s.members[me.Id][cid] == nil {
		s.fail(w, http.StatusForbidden, "api.context.permissions.app_error")
		return
	}
	s.json(w, s.postList(func(p *model.Post) bool { return p.ChannelId == cid && p.IsPinned }))
}

// flagged — GET /users/{uid}/posts/flagged: сохранённые пользователем.
func (s *Server) flagged(w http.ResponseWriter, _ *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	saved := map[string]bool{}
	for _, p := range s.prefs[me.Id] {
		if p.Category == model.PreferenceCategoryFlaggedPost && p.Value == "true" {
			saved[p.Name] = true
		}
	}
	s.json(w, s.postList(func(p *model.Post) bool { return saved[p.Id] && s.members[me.Id][p.ChannelId] != nil }))
}

// postList — посты, где keep, от новых к старым (под s.mu).
func (s *Server) postList(keep func(*model.Post) bool) *model.PostList {
	var ps []*model.Post
	for _, list := range s.posts {
		for _, p := range list {
			if p.DeleteAt == 0 && keep(p) {
				ps = append(ps, p)
			}
		}
	}
	slices.SortFunc(ps, func(a, b *model.Post) int { return int(b.CreateAt - a.CreateAt) })
	out := model.NewPostList()
	for _, p := range ps {
		out.AddPost(p)
		out.AddOrder(p.Id)
	}
	return out
}

// deletePreferences — POST /users/{uid}/preferences/delete; preferences_deleted.
func (s *Server) deletePreferences(w http.ResponseWriter, r *http.Request, me *model.User) {
	var in model.Preferences
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prefs[me.Id] = slices.DeleteFunc(s.prefs[me.Id], func(o model.Preference) bool {
		return slices.ContainsFunc(in, func(p model.Preference) bool { return p.Category == o.Category && p.Name == o.Name })
	})
	s.preferencesEvent(model.WebsocketEventPreferencesDeleted, me.Id, in)
	s.json(w, map[string]string{"status": "OK"})
}

// preferencesEvent — preferences_changed/deleted пользователю (под s.mu).
func (s *Server) preferencesEvent(kind model.WebsocketEventType, userID string, prefs model.Preferences) {
	data, _ := json.Marshal(prefs)
	ev := model.NewWebSocketEvent(kind, "", "", userID, nil, "")
	ev.Add("preferences", string(data))
	s.sendTo(userID, ev)
}

// setUnread — POST /users/{uid}/posts/{pid}/set_unread: канал непрочитан с
// этого поста — счётчики участника как до него, post_unread.
func (s *Server) setUnread(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.post(r.PathValue("pid"))
	if p == nil || s.members[me.Id][p.ChannelId] == nil {
		s.fail(w, http.StatusNotFound, "app.post.get.app_error")
		return
	}
	m := s.members[me.Id][p.ChannelId]
	m.MsgCount, m.MsgCountRoot, m.MentionCount, m.MentionCountRoot = 0, 0, 0, 0
	for _, q := range s.posts[p.ChannelId] {
		if q.CreateAt < p.CreateAt {
			m.MsgCount++
			if q.RootId == "" {
				m.MsgCountRoot++
			}
		}
	}
	m.LastViewedAt = p.CreateAt - 1
	ev := model.NewWebSocketEvent(model.WebsocketEventPostUnread, s.channels[p.ChannelId].TeamId, p.ChannelId, me.Id, nil, "")
	ev.Add("post_id", p.Id)
	ev.Add("msg_count", m.MsgCount)
	ev.Add("msg_count_root", m.MsgCountRoot)
	ev.Add("mention_count", m.MentionCount)
	ev.Add("mention_count_root", m.MentionCountRoot)
	ev.Add("last_viewed_at", m.LastViewedAt)
	s.sendTo(me.Id, ev)
	s.json(w, &model.ChannelUnreadAt{TeamId: s.channels[p.ChannelId].TeamId, ChannelId: p.ChannelId, UserId: me.Id,
		MsgCount: m.MsgCount, MsgCountRoot: m.MsgCountRoot, LastViewedAt: m.LastViewedAt})
}

// threadSetUnread — POST /users/{uid}/teams/{tid}/threads/{thid}/set_unread/{pid}:
// ответы с этого непрочитаны; thread_read_changed.
func (s *Server) threadSetUnread(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, p := r.PathValue("thid"), s.post(r.PathValue("pid"))
	m := s.followers[id][me.Id]
	if m == nil || p == nil {
		s.fail(w, http.StatusNotFound, "app.thread.get_membership_for_user.app_error")
		return
	}
	root := s.post(id)
	m.unreadReplies, m.unreadMentions = 0, 0
	for _, q := range s.posts[root.ChannelId] {
		if q.RootId == id && q.CreateAt >= p.CreateAt {
			m.unreadReplies++
		}
	}
	ev := model.NewWebSocketEvent(model.WebsocketEventThreadReadChanged, r.PathValue("tid"), "", me.Id, nil, "")
	ev.Add("thread_id", id)
	ev.Add("timestamp", p.CreateAt-1)
	ev.Add("unread_mentions", m.unreadMentions)
	ev.Add("unread_replies", m.unreadReplies)
	s.sendTo(me.Id, ev)
	s.json(w, s.threadFor(id, me.Id))
}

// setReminder — POST /users/{uid}/posts/{pid}/reminder: напомнить о посте
// (время — секунды; срабатывание не изображаем, только запоминаем).
func (s *Server) setReminder(w http.ResponseWriter, r *http.Request, me *model.User) {
	var rem model.PostReminder
	if err := json.NewDecoder(r.Body).Decode(&rem); err != nil || r.PathValue("uid") != me.Id || rem.TargetTime <= 0 {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.post(r.PathValue("pid")) == nil {
		s.fail(w, http.StatusNotFound, "app.post.get.app_error")
		return
	}
	s.reminders[r.PathValue("pid")] = rem.TargetTime
	s.json(w, map[string]string{"status": "OK"})
}

// Reminder — когда напомнить о посте (секунды), 0 — не просили; для тестов.
func (s *Server) Reminder(postID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reminders[postID]
}

// Last — последний пост канала; для тестов.
func (s *Server) Last(channelID string) *model.Post {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ps := s.posts[channelID]; len(ps) > 0 {
		return ps[len(ps)-1]
	}
	return nil
}

// EditPost — автор правит пост (как бот, дописывающий «UPD»): всем в канале
// post_edited; для тестов.
func (s *Server) EditPost(id, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.post(id)
	if p == nil {
		return
	}
	p.Message = message
	p.EditAt, p.UpdateAt = model.GetMillis(), model.GetMillis()
	postJSON, _ := json.Marshal(p)
	ev := model.NewWebSocketEvent(model.WebsocketEventPostEdited, "", p.ChannelId, "", nil, "")
	ev.Add("post", string(postJSON))
	s.sendToChannel(p.ChannelId, ev)
}
