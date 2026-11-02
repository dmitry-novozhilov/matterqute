package mmfake

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Отложенные сообщения, как у сервера с лицензией: хранит и отправляет
// сервер (здесь — по ProcessScheduled), владельцу — события
// scheduled_post_created/updated/deleted. NoScheduledPosts — ошибка лицензии.

const scheduledLicenseError = "api.scheduled_posts.license_error"

func (s *Server) scheduledOff(w http.ResponseWriter) bool {
	if s.NoScheduledPosts {
		s.failMessage(w, http.StatusBadRequest, scheduledLicenseError, "Scheduled posts feature requires a license.")
		return true
	}
	return false
}

func (s *Server) createScheduled(w http.ResponseWriter, r *http.Request, me *model.User) {
	if s.scheduledOff(w) {
		return
	}
	var sp model.ScheduledPost
	if err := json.NewDecoder(r.Body).Decode(&sp); err != nil || sp.ChannelId == "" || sp.Message == "" && len(sp.FileIds) == 0 {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	if sp.ScheduledAt < model.GetMillis()-5000 {
		s.failMessage(w, http.StatusBadRequest, "model.scheduled_post.is_valid.scheduled_at.app_error", "Scheduled time is in the past.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.members[me.Id][sp.ChannelId] == nil {
		s.fail(w, http.StatusForbidden, "api.context.permissions.app_error")
		return
	}
	sp.Id, sp.UserId = model.NewId(), me.Id
	sp.CreateAt, sp.UpdateAt = model.GetMillis(), model.GetMillis()
	s.scheduled[sp.Id] = &sp
	s.scheduledEvent(model.WebsocketScheduledPostCreated, &sp)
	w.WriteHeader(http.StatusCreated)
	s.json(w, &sp)
}

// listScheduled — свои отложенные: по команде и личкам (ключ directChannels).
func (s *Server) listScheduled(w http.ResponseWriter, r *http.Request, me *model.User) {
	if s.scheduledOff(w) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tid := r.PathValue("tid")
	out := map[string][]*model.ScheduledPost{tid: {}}
	direct := r.URL.Query().Get("includeDirectChannels") == "true"
	for _, sp := range s.scheduled {
		c := s.channels[sp.ChannelId]
		switch {
		case sp.UserId != me.Id || c == nil:
		case c.TeamId == tid:
			out[tid] = append(out[tid], sp)
		case c.TeamId == "" && direct:
			out["directChannels"] = append(out["directChannels"], sp)
		}
	}
	s.json(w, out)
}

func (s *Server) updateScheduled(w http.ResponseWriter, r *http.Request, me *model.User) {
	if s.scheduledOff(w) {
		return
	}
	var in model.ScheduledPost
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sp := s.scheduled[r.PathValue("id")]
	if sp == nil || sp.UserId != me.Id {
		s.fail(w, http.StatusNotFound, "app.scheduled_post.get.app_error")
		return
	}
	sp.Message, sp.ScheduledAt, sp.FileIds = in.Message, in.ScheduledAt, in.FileIds
	sp.UpdateAt, sp.ErrorCode = model.GetMillis(), ""
	s.scheduledEvent(model.WebsocketScheduledPostUpdated, sp)
	s.json(w, sp)
}

func (s *Server) deleteScheduled(w http.ResponseWriter, r *http.Request, me *model.User) {
	if s.scheduledOff(w) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sp := s.scheduled[r.PathValue("id")]
	if sp == nil || sp.UserId != me.Id {
		s.fail(w, http.StatusNotFound, "app.scheduled_post.get.app_error")
		return
	}
	delete(s.scheduled, sp.Id)
	s.scheduledEvent(model.WebsocketScheduledPostDeleted, sp)
	s.json(w, sp)
}

// scheduledEvent — событие об отложенном его владельцу (под s.mu).
func (s *Server) scheduledEvent(kind model.WebsocketEventType, sp *model.ScheduledPost) {
	data, _ := json.Marshal(sp)
	ev := model.NewWebSocketEvent(kind, "", "", sp.UserId, nil, "")
	ev.Add("scheduledPost", string(data))
	msg, _ := ev.ToJSON()
	for c := range s.conns {
		if c.userID == sp.UserId {
			c.write(msg)
		}
	}
}

// ProcessScheduled — отправить отложенные, чьё время пришло к now (как
// задача сервера): пост в канал, отложенное — прочь.
func (s *Server) ProcessScheduled(now time.Time) {
	s.mu.Lock()
	var due []*model.ScheduledPost
	for id, sp := range s.scheduled {
		if sp.ScheduledAt <= now.UnixMilli() {
			due = append(due, sp)
			delete(s.scheduled, id)
			s.scheduledEvent(model.WebsocketScheduledPostDeleted, sp)
		}
	}
	s.mu.Unlock()
	for _, sp := range due {
		s.Post(&model.Post{ChannelId: sp.ChannelId, RootId: sp.RootId, UserId: sp.UserId, Message: sp.Message, FileIds: sp.FileIds})
	}
}

// Scheduled — сколько отложенных хранит сервер (для тестов).
func (s *Server) Scheduled() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.scheduled)
}
