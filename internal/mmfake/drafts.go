package mmfake

import (
	"encoding/json"
	"net/http"

	"github.com/mattermost/mattermost/server/public/model"
)

// Черновики (/api/v4/drafts): у пользователя по одному на канал и тред;
// изменения — событиями draft_created/updated/deleted всем его соединениям.

func draftID(userID, channelID, rootID string) string { return userID + "/" + channelID + "/" + rootID }

// upsertDraft — POST /drafts.
func (s *Server) upsertDraft(w http.ResponseWriter, r *http.Request, me *model.User) {
	var d model.Draft
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.NoSyncedDrafts {
		s.fail(w, http.StatusNotImplemented, "api.drafts.disabled.app_error")
		return
	}
	if s.members[me.Id][d.ChannelId] == nil {
		s.fail(w, http.StatusForbidden, "api.context.permissions.app_error")
		return
	}
	id := draftID(me.Id, d.ChannelId, d.RootId)
	kind := model.WebsocketEventDraftUpdated
	old := s.drafts[id]
	stored := &model.Draft{UserId: me.Id, ChannelId: d.ChannelId, RootId: d.RootId, Message: d.Message, UpdateAt: model.GetMillis()}
	if old == nil {
		kind, stored.CreateAt = model.WebsocketEventDraftCreated, stored.UpdateAt
	} else {
		stored.CreateAt = old.CreateAt
	}
	s.drafts[id] = stored
	s.draftEvent(kind, stored)
	s.json(w, stored)
}

// listDrafts — GET /users/{uid}/teams/{tid}/drafts: черновики в каналах команды и в личках.
func (s *Server) listDrafts(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.NoSyncedDrafts {
		s.fail(w, http.StatusNotImplemented, "api.drafts.disabled.app_error")
		return
	}
	out := []*model.Draft{}
	for _, d := range s.drafts {
		c := s.channels[d.ChannelId]
		if d.UserId == me.Id && c != nil && (c.TeamId == r.PathValue("tid") || c.TeamId == "") {
			out = append(out, d)
		}
	}
	s.json(w, out)
}

// deleteDraft — DELETE /users/{uid}/channels/{cid}/drafts[/{rid}].
func (s *Server) deleteDraft(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := draftID(me.Id, r.PathValue("cid"), r.PathValue("rid"))
	d := s.drafts[id]
	if d == nil {
		s.fail(w, http.StatusNotFound, "app.drafts.get_drafts.app_error")
		return
	}
	delete(s.drafts, id)
	s.draftEvent(model.WebsocketEventDraftDeleted, d)
	s.json(w, d)
}

// SetDraft — черновик, написанный «в другом клиенте» (для тестов): как POST /drafts.
func (s *Server) SetDraft(userID, channelID, rootID, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := &model.Draft{UserId: userID, ChannelId: channelID, RootId: rootID, Message: message, CreateAt: model.GetMillis(), UpdateAt: model.GetMillis()}
	s.drafts[draftID(userID, channelID, rootID)] = d
	s.draftEvent(model.WebsocketEventDraftUpdated, d)
}

// Draft — сохранённый черновик ("" — нет).
func (s *Server) Draft(userID, channelID, rootID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d := s.drafts[draftID(userID, channelID, rootID)]; d != nil {
		return d.Message
	}
	return ""
}

// draftEvent — событие о черновике всем соединениям пользователя (под s.mu).
func (s *Server) draftEvent(kind model.WebsocketEventType, d *model.Draft) {
	data, _ := json.Marshal(d)
	ev := model.NewWebSocketEvent(kind, "", d.ChannelId, d.UserId, nil, "")
	ev.Add("draft", string(data))
	s.sendTo(d.UserId, ev)
}
