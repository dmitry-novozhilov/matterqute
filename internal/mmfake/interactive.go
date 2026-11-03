package mmfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// Интерактивные сообщения: у алерта Grafana — кнопки и список, как у
// настоящих интеграций; что сделала бы интеграция, делает сам фейк:
// «Принять» правит пост (post_edited), «Заглушить» отвечает «только вам»,
// «Комментарий…» открывает окно с полями (open_dialog), его отправка —
// /actions/dialogs/submit (пустой комментарий — ошибка поля).

// ActionCall — нажатие кнопки или выбор в списке.
type ActionCall struct {
	PostID, ActionID, Selected, Cookie string
}

// alertActions — кнопки алерта.
func alertActions() []*model.PostAction {
	return []*model.PostAction{
		{Id: "ack", Type: model.PostActionTypeButton, Name: "Принять", Style: "primary", Cookie: "cookie-ack"},
		{Id: "silence", Type: model.PostActionTypeSelect, Name: "Заглушить", Options: []*model.PostActionOptions{
			{Text: "на 1 час", Value: "1h"}, {Text: "на 4 часа", Value: "4h"}, {Text: "до завтра", Value: "1d"}}},
		{Id: "comment", Type: model.PostActionTypeButton, Name: "Комментарий…"},
		{Id: "stale", Type: model.PostActionTypeButton, Name: "Устарело", Disabled: true},
	}
}

// commentDialog — окно «Комментарий» к алерту.
func commentDialog(postID string) model.Dialog {
	return model.Dialog{
		CallbackId: "comment", Title: "Комментарий к алерту", State: postID,
		IntroductionText: "Что сделано с **DiskSpaceLow**?", SubmitLabel: "Сохранить", NotifyOnCancel: true,
		Elements: []model.DialogElement{
			{DisplayName: "Комментарий", Name: "comment", Type: "textarea", Placeholder: "Что сделали"},
			{DisplayName: "Важность", Name: "severity", Type: "select", Default: "high", Options: []*model.PostActionOptions{
				{Text: "Низкая", Value: "low"}, {Text: "Высокая", Value: "high"}}},
			{DisplayName: "Уведомить дежурного", Name: "notify", Type: "bool", Placeholder: "Отправить в #oncall", Optional: true},
			{DisplayName: "Время", Name: "minutes", Type: "text", SubType: "number", Optional: true},
		},
	}
}

// doAction — POST /posts/{pid}/actions/{aid}.
func (s *Server) doAction(w http.ResponseWriter, r *http.Request, me *model.User) {
	var req model.DoPostActionRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.post(r.PathValue("pid"))
	if p == nil || s.members[me.Id][p.ChannelId] == nil {
		s.fail(w, http.StatusNotFound, "app.post.get.app_error")
		return
	}
	id := r.PathValue("aid")
	a := p.GetAction(id)
	if a == nil {
		s.fail(w, http.StatusNotFound, "api.post.do_action.action_id.app_error")
		return
	}
	s.actions = append(s.actions, ActionCall{PostID: p.Id, ActionID: id, Selected: req.SelectedOption, Cookie: req.Cookie})
	resp := model.PostActionAPIResponse{Status: "OK"}
	switch id {
	case "ack":
		atts := p.Attachments()
		for _, att := range atts {
			att.Text += "\n\nПринял: @" + me.Username
			att.Actions = slices.DeleteFunc(att.Actions, func(x *model.PostAction) bool { return x.Id == "ack" })
		}
		p.AddProp(model.PostPropsAttachments, atts)
		p.UpdateAt = model.GetMillis()
		postJSON, _ := json.Marshal(p)
		ev := model.NewWebSocketEvent(model.WebsocketEventPostEdited, "", p.ChannelId, "", nil, "")
		ev.Add("post", string(postJSON))
		s.sendToChannel(p.ChannelId, ev)
	case "silence":
		s.ephemeral(me.Id, &model.CommandArgs{ChannelId: p.ChannelId}, "Заглушено: "+req.SelectedOption)
	case "comment":
		resp.TriggerId = model.NewId()
		raw, _ := json.Marshal(model.OpenDialogRequest{TriggerId: resp.TriggerId, URL: "https://hooks.example.com/comment",
			Dialog: commentDialog(p.Id), ChannelId: p.ChannelId})
		ev := model.NewWebSocketEvent(model.WebsocketEventOpenDialog, "", "", me.Id, nil, "")
		ev.Add("dialog", string(raw))
		s.sendTo(me.Id, ev)
	}
	s.json(w, resp)
}

// submitDialog — POST /actions/dialogs/submit.
func (s *Server) submitDialog(w http.ResponseWriter, r *http.Request, me *model.User) {
	var req model.SubmitDialogRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dialogs = append(s.dialogs, req)
	if req.Cancelled {
		s.json(w, model.SubmitDialogResponse{})
		return
	}
	comment, _ := req.Submission["comment"].(string)
	if strings.TrimSpace(comment) == "" {
		s.json(w, model.SubmitDialogResponse{Errors: map[string]string{"comment": "Напишите, что сделали"}})
		return
	}
	s.ephemeral(me.Id, &model.CommandArgs{ChannelId: req.ChannelId}, fmt.Sprintf("Комментарий сохранён: %s", comment))
	s.json(w, model.SubmitDialogResponse{})
}

// Actions — нажатия кнопок и выборы в списках (для тестов).
func (s *Server) Actions() []ActionCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.actions)
}

// Dialogs — отправленные окна интеграций, с отменами (для тестов).
func (s *Server) Dialogs() []model.SubmitDialogRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.dialogs)
}
