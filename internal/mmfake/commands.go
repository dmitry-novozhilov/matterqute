package mmfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// Слэш-команды: несколько встроенных, как у сервера, — для разработки и
// тестов. Ответ «только вам» — событием ephemeral_message, как у сервера;
// переход — goto_location в ответе.
var commands = []struct{ trigger, hint, desc string }{
	{"away", "", "Статус «Отошёл»"},
	{"echo", "текст", "Повторить текст сообщением"},
	{"join", "~канал", "Войти в канал"},
	{"me", "действие", "Сообщение от третьего лица"},
	{"online", "", "Статус «В сети»"},
	{"shrug", "текст", `Текст и ¯\_(ツ)_/¯`},
}

func (s *Server) executeCommand(w http.ResponseWriter, r *http.Request, me *model.User) {
	var args model.CommandArgs
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	trigger, rest, _ := strings.Cut(strings.TrimPrefix(args.Command, "/"), " ")
	rest = strings.TrimSpace(rest)
	switch trigger {
	case "away", "online":
		status, text := model.StatusAway, "Вы отошли."
		if trigger == "online" {
			status, text = model.StatusOnline, "Вы в сети."
		}
		s.mu.Lock()
		s.statuses[me.Id] = status
		ev := model.NewWebSocketEvent(model.WebsocketEventStatusChange, "", "", "", nil, "")
		ev.Add("user_id", me.Id)
		ev.Add("status", status)
		s.broadcast(ev)
		s.ephemeral(me.Id, &args, text)
		s.mu.Unlock()
		s.json(w, &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: text})
	case "echo", "me", "shrug":
		msg := rest
		switch trigger {
		case "me":
			msg = "*" + rest + "*"
		case "shrug":
			msg = strings.TrimSpace(rest + ` ¯\\\_(ツ)\_/¯`)
		}
		if msg == "" {
			s.failMessage(w, http.StatusBadRequest, "api.command.execute_command.empty", "Нечего отправить.")
			return
		}
		s.Post(&model.Post{ChannelId: args.ChannelId, RootId: args.RootId, UserId: me.Id, Message: msg})
		s.json(w, &model.CommandResponse{ResponseType: model.CommandResponseTypeInChannel})
	case "join":
		name := strings.TrimPrefix(rest, "~")
		s.mu.Lock()
		var ch *model.Channel
		for _, c := range s.channels {
			if c.Name == name && c.TeamId == args.TeamId && c.Type == model.ChannelTypeOpen && c.DeleteAt == 0 {
				ch = c
			}
		}
		if ch == nil {
			s.mu.Unlock()
			s.failMessage(w, http.StatusBadRequest, "api.command_join.list.app_error", fmt.Sprintf("Канала «%s» нет.", name))
			return
		}
		if s.members[me.Id][ch.Id] == nil {
			s.addChannel(ch, []*model.User{me})
		}
		team := ""
		for _, t := range s.teams {
			if t.Id == ch.TeamId {
				team = t.Name
			}
		}
		s.mu.Unlock()
		s.json(w, &model.CommandResponse{GotoLocation: "/" + team + "/channels/" + ch.Name})
	default:
		s.failMessage(w, http.StatusNotFound, "api.command.execute_command.not_found.app_error",
			fmt.Sprintf("Command with a trigger of '/%s' not found.", trigger))
	}
}

// ephemeral — сообщение «только вам» по WebSocket (под s.mu).
func (s *Server) ephemeral(userID string, args *model.CommandArgs, text string) {
	p := &model.Post{Id: model.NewId(), ChannelId: args.ChannelId, RootId: args.RootId, UserId: userID,
		Type: model.PostTypeEphemeral, Message: text, CreateAt: model.GetMillis()}
	postJSON, _ := json.Marshal(p)
	ev := model.NewWebSocketEvent(model.WebsocketEventEphemeralMessage, "", args.ChannelId, userID, nil, "")
	ev.Add("post", string(postJSON))
	data, _ := ev.ToJSON()
	for c := range s.conns {
		if c.userID == userID {
			c.write(data)
		}
	}
}

// commandSuggestions — подсказки: имена команд, а после «/join » — каналы.
func (s *Server) commandSuggestions(w http.ResponseWriter, r *http.Request, me *model.User) {
	input := strings.TrimPrefix(r.URL.Query().Get("user_input"), "/")
	out := []model.AutocompleteSuggestion{}
	if trigger, arg, args := strings.Cut(input, " "); !args {
		for _, c := range commands {
			if strings.HasPrefix(c.trigger, input) {
				out = append(out, model.AutocompleteSuggestion{Complete: c.trigger, Suggestion: c.trigger, Hint: c.hint, Description: c.desc})
			}
		}
	} else if trigger == "join" {
		s.mu.Lock()
		for _, c := range s.channels {
			if c.TeamId == r.PathValue("tid") && c.Type == model.ChannelTypeOpen && strings.HasPrefix(c.Name, strings.TrimPrefix(arg, "~")) {
				out = append(out, model.AutocompleteSuggestion{Complete: "join " + c.Name, Suggestion: c.Name, Description: c.DisplayName})
			}
		}
		s.mu.Unlock()
	}
	s.json(w, out)
}

func (s *Server) failMessage(w http.ResponseWriter, status int, id, message string) {
	e := model.NewAppError("mmfake", id, nil, "", status)
	e.Message = message
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(e)
}

// channelByName — канал команды по именам: открытый или тот, где пользователь есть.
func (s *Server) channelByName(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	team := ""
	for _, t := range s.teams {
		if t.Name == r.PathValue("team") {
			team = t.Id
		}
	}
	for _, c := range s.channels {
		if c.DeleteAt > 0 && r.URL.Query().Get("include_deleted") != "true" {
			continue
		}
		if c.TeamId == team && c.Name == r.PathValue("channel") && (c.Type == model.ChannelTypeOpen || s.members[me.Id][c.Id] != nil) {
			s.json(w, c)
			return
		}
	}
	s.fail(w, http.StatusNotFound, "app.channel.get_by_name.missing.app_error")
}

// getPost — пост по id, если он в канале пользователя.
func (s *Server) getPost(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.post(r.PathValue("pid"))
	if p == nil || !s.readable(me, s.channels[p.ChannelId]) {
		s.fail(w, http.StatusNotFound, "app.post.get.app_error")
		return
	}
	s.json(w, p)
}
