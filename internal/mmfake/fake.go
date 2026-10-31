// Package mmfake — минимальный фейковый сервер Mattermost для разработки UI
// и тестов: вход (с MFA), команды, каналы, пользователи и WebSocket с hello.
package mmfake

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mattermost/mattermost/server/public/model"
)

// Учётки: alice/secret — без MFA, bob/secret — с MFA-кодом MFACode.
const (
	Password = "secret"
	MFACode  = "123456"
)

type Server struct {
	log *slog.Logger
	mux *http.ServeMux

	mu       sync.Mutex
	users    map[string]*model.User // по id
	mfa      map[string]bool        // по username
	sessions map[string]string      // token → user id
	teams    []*model.Team
	channels map[string][]*model.Channel // по id команды
}

func New(log *slog.Logger) *Server {
	s := &Server{
		log:      log,
		mux:      http.NewServeMux(),
		users:    map[string]*model.User{},
		mfa:      map[string]bool{"bob": true},
		sessions: map[string]string{},
		channels: map[string][]*model.Channel{},
	}
	s.seed()
	s.mux.HandleFunc("POST /api/v4/users/login", s.login)
	s.mux.HandleFunc("POST /api/v4/users/logout", s.auth(s.logout))
	s.mux.HandleFunc("GET /api/v4/users/me", s.auth(s.me))
	s.mux.HandleFunc("GET /api/v4/users/{uid}/teams", s.auth(s.userTeams))
	s.mux.HandleFunc("GET /api/v4/users/{uid}/teams/{tid}/channels", s.auth(s.teamChannels))
	s.mux.HandleFunc("POST /api/v4/users/ids", s.auth(s.usersByIDs))
	s.mux.HandleFunc("GET /api/v4/websocket", s.websocket)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.log.Debug("request", "method", r.Method, "path", r.URL.Path)
	s.mux.ServeHTTP(w, r)
}

func (s *Server) seed() {
	for _, name := range []string{"alice", "bob", "carol", "dave"} {
		u := &model.User{Id: model.NewId(), Username: name, Email: name + "@example.com"}
		s.users[u.Id] = u
	}
	alice, bob, carol := s.byName("alice"), s.byName("bob"), s.byName("carol")

	dev := &model.Team{Id: model.NewId(), Name: "dev", DisplayName: "Разработка"}
	ops := &model.Team{Id: model.NewId(), Name: "ops", DisplayName: "Эксплуатация"}
	s.teams = []*model.Team{dev, ops}

	ch := func(team *model.Team, typ model.ChannelType, name, display, purpose string) *model.Channel {
		return &model.Channel{Id: model.NewId(), TeamId: team.Id, Type: typ, Name: name, DisplayName: display, Purpose: purpose}
	}
	dm := func(a, b *model.User) *model.Channel {
		return &model.Channel{Id: model.NewId(), Type: model.ChannelTypeDirect, Name: model.GetDMNameFromIds(a.Id, b.Id)}
	}
	direct := []*model.Channel{
		dm(alice, bob), dm(alice, carol), dm(alice, alice),
		{Id: model.NewId(), Type: model.ChannelTypeGroup, Name: model.NewId(), DisplayName: "alice, bob, carol"},
	}
	s.channels[dev.Id] = append([]*model.Channel{
		ch(dev, model.ChannelTypeOpen, "town-square", "Town Square", "Общий канал команды"),
		ch(dev, model.ChannelTypeOpen, "backend", "backend", "Сервисы и API"),
		ch(dev, model.ChannelTypePrivate, "releases", "Релизы", "Планирование релизов"),
	}, direct...)
	s.channels[ops.Id] = append([]*model.Channel{
		ch(ops, model.ChannelTypeOpen, "town-square", "Town Square", ""),
		ch(ops, model.ChannelTypeOpen, "alerts", "alerts", "Алерты из Grafana"),
	}, direct...)
}

func (s *Server) byName(name string) *model.User {
	for _, u := range s.users {
		if u.Username == name {
			return u
		}
	}
	return nil
}

func (s *Server) fail(w http.ResponseWriter, status int, id string) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(model.NewAppError("mmfake", id, nil, "", status))
}

func (s *Server) json(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	login := strings.TrimSuffix(body["login_id"], "@example.com")
	u := s.byName(login)
	switch {
	case u == nil || body["password"] != Password:
		s.fail(w, http.StatusUnauthorized, "api.user.login.invalid_credentials_email_username")
		return
	case s.mfa[u.Username] && body["token"] == "":
		s.fail(w, http.StatusBadRequest, "mfa.validate_token.authenticate.app_error")
		return
	case s.mfa[u.Username] && body["token"] != MFACode:
		s.fail(w, http.StatusUnauthorized, "api.user.check_user_mfa.bad_code.app_error")
		return
	}
	token := model.NewId()
	s.sessions[token] = u.Id
	w.Header().Set(model.HeaderToken, token)
	s.json(w, u)
}

type authed func(w http.ResponseWriter, r *http.Request, me *model.User)

func (s *Server) auth(h authed) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get(model.HeaderAuth), model.HeaderBearer+" ")
		s.mu.Lock()
		me := s.users[s.sessions[token]]
		s.mu.Unlock()
		if me == nil {
			s.fail(w, http.StatusUnauthorized, "api.context.session_expired.app_error")
			return
		}
		h(w, r, me)
	}
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, _ *model.User) {
	token := strings.TrimPrefix(r.Header.Get(model.HeaderAuth), model.HeaderBearer+" ")
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
	s.json(w, map[string]string{"status": "OK"})
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request, me *model.User) { s.json(w, me) }

func (s *Server) userTeams(w http.ResponseWriter, _ *http.Request, _ *model.User) {
	s.json(w, s.teams)
}

func (s *Server) teamChannels(w http.ResponseWriter, r *http.Request, _ *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chs, ok := s.channels[r.PathValue("tid")]
	if !ok {
		s.fail(w, http.StatusNotFound, "app.team.get.find.app_error")
		return
	}
	s.json(w, chs)
}

func (s *Server) usersByIDs(w http.ResponseWriter, r *http.Request, _ *model.User) {
	var ids []string
	if err := json.NewDecoder(r.Body).Decode(&ids); err != nil {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*model.User{}
	for _, id := range ids {
		if u := s.users[id]; u != nil {
			out = append(out, u)
		}
	}
	s.json(w, out)
}

var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

// websocket: ждёт authentication_challenge, отвечает OK и шлёт hello, дальше держит пинги.
func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Warn("websocket upgrade", "err", err)
		return
	}
	defer conn.Close()

	var req model.WebSocketRequest
	if err := conn.ReadJSON(&req); err != nil {
		s.log.Warn("websocket read challenge", "err", err)
		return
	}
	token, _ := req.Data["token"].(string)
	s.mu.Lock()
	uid := s.sessions[token]
	s.mu.Unlock()
	if req.Action != string(model.WebsocketAuthenticationChallenge) || uid == "" {
		conn.WriteJSON(&model.WebSocketResponse{Status: "FAIL", SeqReply: req.Seq,
			Error: model.NewAppError("mmfake", "api.web_socket_router.not_authenticated.app_error", nil, "", http.StatusUnauthorized)})
		return
	}
	conn.WriteJSON(model.NewWebSocketResponse("OK", req.Seq, nil))
	hello := model.NewWebSocketEvent(model.WebsocketEventHello, "", "", uid, nil, "")
	hello.Add("server_version", "mmfake")
	data, _ := hello.ToJSON()
	conn.WriteMessage(websocket.TextMessage, data)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-done:
			return
		case <-ping.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		}
	}
}
