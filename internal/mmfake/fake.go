// Package mmfake — минимальный фейковый сервер Mattermost для разработки UI
// и тестов: вход (с MFA), команды, каналы, членства, посты, треды, реакции
// и WebSocket.
package mmfake

import (
	"encoding/json"
	"fmt"
	"image/color"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
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

	// CollapsedThreads — режим CRT в конфиге сервера; менять до первых запросов.
	CollapsedThreads string
	// NoScheduledPosts — сервер без отложенных сообщений (как без лицензии):
	// их API отвечает ошибкой лицензии.
	NoScheduledPosts bool
	scheduled        map[string]*model.ScheduledPost
	typed            []TypingEvent

	mu        sync.Mutex
	users     map[string]*model.User // по id
	mfa       map[string]bool        // по username
	sessions  map[string]string      // token → user id
	teams     []*model.Team
	channels  map[string]*model.Channel
	members   map[string]map[string]*model.ChannelMember // user id → channel id →
	posts     map[string][]*model.Post                   // по каналу, от старых к новым
	followers map[string]map[string]*threadMember        // корень треда → user id →
	files     map[string]*storedFile
	emoji     []*model.Emoji
	emojiData map[string][]byte            // id эмодзи → картинка
	statuses  map[string]string            // user id → online, away, dnd, offline
	dndEnd    map[string]int64             // user id → конец «не беспокоить», секунды
	prefs     map[string]model.Preferences // user id → настройки
	conns     map[*wsConn]bool
}

type wsConn struct {
	userID string
	mu     sync.Mutex
	c      *websocket.Conn
}

func (c *wsConn) write(v []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.c.WriteMessage(websocket.TextMessage, v)
}

func New(log *slog.Logger) *Server {
	s := &Server{
		log:       log,
		mux:       http.NewServeMux(),
		users:     map[string]*model.User{},
		mfa:       map[string]bool{"bob": true},
		sessions:  map[string]string{},
		channels:  map[string]*model.Channel{},
		members:   map[string]map[string]*model.ChannelMember{},
		posts:     map[string][]*model.Post{},
		conns:     map[*wsConn]bool{},
		scheduled: map[string]*model.ScheduledPost{},

		CollapsedThreads: model.CollapsedThreadsAlwaysOn,
		followers:        map[string]map[string]*threadMember{},
		files:            map[string]*storedFile{},
		emojiData:        map[string][]byte{},
		statuses:         map[string]string{},
		dndEnd:           map[string]int64{},
		prefs:            map[string]model.Preferences{},
	}
	s.seed()
	s.mux.HandleFunc("POST /api/v4/users/login", s.login)
	s.mux.HandleFunc("POST /api/v4/users/logout", s.auth(s.logout))
	s.mux.HandleFunc("GET /api/v4/users/me", s.auth(s.me))
	s.mux.HandleFunc("GET /api/v4/users/autocomplete", s.auth(s.usersAutocomplete))
	s.mux.HandleFunc("GET /api/v4/emoji/autocomplete", s.auth(s.emojiAutocomplete))
	s.mux.HandleFunc("GET /api/v4/users/{uid}/teams", s.auth(s.userTeams))
	s.mux.HandleFunc("GET /api/v4/users/{uid}/teams/{tid}/channels", s.auth(s.teamChannels))
	s.mux.HandleFunc("GET /api/v4/users/{uid}/teams/{tid}/channels/members", s.auth(s.teamMembers))
	s.mux.HandleFunc("POST /api/v4/users/ids", s.auth(s.usersByIDs))
	s.mux.HandleFunc("GET /api/v4/channels/{cid}/posts", s.auth(s.channelPosts))
	s.mux.HandleFunc("POST /api/v4/channels/members/{uid}/view", s.auth(s.viewChannel))
	s.mux.HandleFunc("POST /api/v4/posts", s.auth(s.createPost))
	s.mux.HandleFunc("GET /api/v4/posts/{pid}/thread", s.auth(s.postThread))
	s.mux.HandleFunc("GET /api/v4/config/client", s.auth(s.clientConfig))
	s.mux.HandleFunc("GET /api/v4/users/{uid}/preferences/{category}", s.auth(s.preferences))
	s.mux.HandleFunc("PUT /api/v4/users/{uid}/preferences", s.auth(s.savePreferences))
	s.mux.HandleFunc("GET /api/v4/emoji", s.auth(s.emojiList))
	s.mux.HandleFunc("GET /api/v4/users/{uid}/teams/{tid}/threads", s.auth(s.userThreads))
	s.mux.HandleFunc("PUT /api/v4/users/{uid}/teams/{tid}/threads/{thid}/read/{ts}", s.auth(s.threadRead))
	s.mux.HandleFunc("POST /api/v4/reactions", s.auth(s.saveReaction))
	s.mux.HandleFunc("DELETE /api/v4/users/{uid}/posts/{pid}/reactions/{emoji}", s.auth(s.deleteReaction))
	s.mux.HandleFunc("POST /api/v4/files", s.auth(s.uploadFile))
	s.mux.HandleFunc("GET /api/v4/files/{fid}", s.auth(s.getFile))
	s.mux.HandleFunc("GET /api/v4/files/{fid}/preview", s.auth(s.getFile))
	s.mux.HandleFunc("GET /api/v4/files/{fid}/thumbnail", s.auth(s.getFile))
	// /emoji/name/{name} и /emoji/{id}/image: ServeMux считает их конфликтующими.
	s.mux.HandleFunc("GET /api/v4/emoji/{a}/{b}", s.auth(s.emojiRoute))
	s.mux.HandleFunc("POST /api/v4/users/status/ids", s.auth(s.statusesByIDs))
	// /users/username/{name} и /users/{id}/status — одним обработчиком: иначе ServeMux видит конфликт.
	s.mux.HandleFunc("GET /api/v4/users/{a}/{b}", s.auth(s.userRoute))
	s.mux.HandleFunc("POST /api/v4/channels/direct", s.auth(s.createDirect))
	s.mux.HandleFunc("GET /api/v4/teams/{tid}/channels", s.auth(s.publicChannels))
	s.mux.HandleFunc("POST /api/v4/channels", s.auth(s.createChannel))
	s.mux.HandleFunc("POST /api/v4/channels/{cid}/members", s.auth(s.joinChannel))
	s.mux.HandleFunc("DELETE /api/v4/channels/{cid}/members/{uid}", s.auth(s.leaveChannel))
	s.mux.HandleFunc("GET /api/v4/channels/{cid}/members/{uid}", s.auth(s.channelMember))
	s.mux.HandleFunc("PUT /api/v4/channels/{cid}/members/{uid}/notify_props", s.auth(s.memberNotifyProps))
	s.mux.HandleFunc("GET /api/v4/websocket", s.websocket)
	// Иконка интеграции на «чужом» сервере: без авторизации, как у Grafana.
	// Картинки «чужих» сайтов: иконка интеграции, картинки предпросмотра ссылок.
	// Без авторизации, как на настоящем стороннем сервере.
	s.mux.HandleFunc("GET /static/{name}", func(w http.ResponseWriter, r *http.Request) {
		var data []byte
		switch r.PathValue("name") {
		case "integration.png":
			data = circlePNG(64, color.RGBA{244, 104, 0, 255})
		case "og.png":
			data = gradientPNG(1200, 630)
		case "photo.png":
			data = gradientPNG(640, 360)
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(data)
	})
	// PUT /posts/{pid}/patch и /posts/schedule/{id} — один шаблон: иначе
	// маршрутизатор не решит, чей «/posts/schedule/patch».
	s.mux.HandleFunc("PUT /api/v4/posts/{a}/{b}", s.auth(func(w http.ResponseWriter, r *http.Request, me *model.User) {
		switch a, b := r.PathValue("a"), r.PathValue("b"); {
		case a == "schedule":
			r.SetPathValue("id", b)
			s.updateScheduled(w, r, me)
		case b == "patch":
			r.SetPathValue("pid", a)
			s.patchPost(w, r, me)
		default:
			http.NotFound(w, r)
		}
	}))
	s.mux.HandleFunc("DELETE /api/v4/posts/{pid}", s.auth(s.deletePost))
	s.mux.HandleFunc("POST /api/v4/commands/execute", s.auth(s.executeCommand))
	s.mux.HandleFunc("POST /api/v4/posts/schedule", s.auth(s.createScheduled))
	s.mux.HandleFunc("POST /api/v4/posts/search", s.auth(s.searchPosts))
	s.mux.HandleFunc("POST /api/v4/teams/{tid}/posts/search", s.auth(s.searchPosts))
	s.mux.HandleFunc("GET /api/v4/posts/scheduled/team/{tid}", s.auth(s.listScheduled))
	s.mux.HandleFunc("DELETE /api/v4/posts/schedule/{id}", s.auth(s.deleteScheduled))
	s.mux.HandleFunc("GET /api/v4/teams/{tid}/commands/autocomplete_suggestions", s.auth(s.commandSuggestions))
	s.mux.HandleFunc("GET /api/v4/teams/name/{team}/channels/name/{channel}", s.auth(s.channelByName))
	s.mux.HandleFunc("GET /api/v4/posts/{pid}", s.auth(s.getPost))
	s.mux.HandleFunc("PUT /api/v4/users/{uid}/status", s.auth(s.setStatus))
	s.mux.HandleFunc("PUT /api/v4/users/{uid}/status/custom", s.auth(s.setCustomStatus))
	s.mux.HandleFunc("DELETE /api/v4/users/{uid}/status/custom", s.auth(s.setCustomStatus))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.log.Debug("request", "method", r.Method, "path", r.URL.Path, "query", r.URL.RawQuery)
	s.mux.ServeHTTP(w, r)
}

func (s *Server) seed() {
	for _, p := range []struct{ name, first, last, position, status string }{
		{"alice", "Алиса", "Ковалёва", "Разработчица", model.StatusOnline},
		{"bob", "Борис", "Иванов", "", model.StatusOnline},
		{"carol", "Карина", "Орлова", "Тимлид", model.StatusAway},
		{"dave", "Денис", "Ветров", "Инженер эксплуатации", model.StatusDnd},
	} {
		u := &model.User{Id: model.NewId(), Username: p.name, Email: p.name + "@example.com", FirstName: p.first, LastName: p.last, Position: p.position}
		s.users[u.Id] = u
		s.statuses[u.Id] = p.status
	}
	alice, bob, carol, dave := s.byName("alice"), s.byName("bob"), s.byName("carol"), s.byName("dave")
	everyone := []*model.User{alice, bob, carol, dave}
	carol.SetCustomStatus(&model.CustomStatus{Emoji: "calendar", Text: "На встрече"})
	// erin только в Town Square команды dev и ничего не писала: клиент узнаёт
	// о ней лишь из поиска пользователей (автодополнение @).
	erin := &model.User{Id: model.NewId(), Username: "erin", Email: "erin@example.com", FirstName: "Эрин", LastName: "Смирнова"}
	s.users[erin.Id] = erin
	s.statuses[erin.Id] = model.StatusOffline

	dev := &model.Team{Id: model.NewId(), Name: "dev", DisplayName: "Разработка"}
	ops := &model.Team{Id: model.NewId(), Name: "ops", DisplayName: "Эксплуатация"}
	s.teams = []*model.Team{dev, ops}

	ch := func(team *model.Team, typ model.ChannelType, name, display, purpose string) *model.Channel {
		c := &model.Channel{Id: model.NewId(), TeamId: team.Id, Type: typ, Name: name, DisplayName: display, Purpose: purpose}
		s.addChannel(c, everyone)
		return c
	}
	dm := func(a, b *model.User) *model.Channel {
		c := &model.Channel{Id: model.NewId(), Type: model.ChannelTypeDirect, Name: model.GetDMNameFromIds(a.Id, b.Id)}
		s.addChannel(c, []*model.User{a, b})
		return c
	}
	town := ch(dev, model.ChannelTypeOpen, "town-square", "Town Square", "Общий канал команды")
	s.addChannel(town, []*model.User{erin})
	backend := ch(dev, model.ChannelTypeOpen, "backend", "backend", "Сервисы и API")
	ch(dev, model.ChannelTypePrivate, "releases", "Релизы", "Планирование релизов")
	// Открытый канал без alice — войти командой /join.
	s.addChannel(&model.Channel{Id: model.NewId(), TeamId: dev.Id, Type: model.ChannelTypeOpen, Name: "frontend",
		DisplayName: "frontend", Purpose: "Веб-клиент"}, []*model.User{bob, carol})
	ch(ops, model.ChannelTypeOpen, "town-square", "Town Square", "")
	alerts := ch(ops, model.ChannelTypeOpen, "alerts", "alerts", "Алерты из Grafana")
	withBob := dm(alice, bob)
	dm(alice, carol)
	dm(alice, alice)
	gm := &model.Channel{Id: model.NewId(), Type: model.ChannelTypeGroup, Name: model.NewId(), DisplayName: "alice, bob, carol"}
	s.addChannel(gm, []*model.User{alice, bob, carol})

	// История: 150 постов в town-square для проверки подгрузки, всё прочитано.
	now := time.Now()
	start := now.Add(-150 * 7 * time.Minute)
	lines := []string{
		"Доброе утро!",
		"Кто-нибудь смотрел вчерашний релиз?",
		"Да, всё ок, графики ровные.\nНо p99 на /api/v4/posts подрос, гляньте https://grafana.example.com/d/abc?from=now-1d.",
		"Ок, посмотрю после обеда.",
		"Напоминаю: в 15:00 созвон по планированию.",
	}
	for i := range 150 {
		u := everyone[i%len(everyone)]
		at := start.Add(time.Duration(i) * 7 * time.Minute)
		if i%10 == 3 {
			at = start.Add(time.Duration(i-1)*7*time.Minute + time.Minute) // продолжение того же автора
			u = everyone[(i-1)%len(everyone)]
		}
		s.seedPost(town, u, at, fmt.Sprintf("%s (#%d)", lines[i%len(lines)], i+1))
	}
	s.seedPost(town, dave, now.Add(-time.Minute), "").Type = model.PostTypeJoinChannel
	s.posts[town.Id][len(s.posts[town.Id])-1].Message = "dave joined the channel."

	// Разметка, картинка и файл — в backend, алерт с вложением — в alerts.
	e := &model.Emoji{Id: model.NewId(), Name: "partyparrot"}
	s.emoji = append(s.emoji, e)
	s.emojiData[e.Id] = partyGIF(32) // анимированное, как настоящий partyparrot
	s.seedPost(backend, carol, now.Add(-40*time.Minute), "### Релиз 2.4 :partyparrot:\n"+
		"Что вошло:\n- [x] **новый** кэш сессий\n- [ ] ~~старый~~ API — *в следующем*\n\n"+
		"| сервис | p99 |\n|---|---|\n| api | 120 мс |\n| ws | 35 мс |\n\n"+
		"Конфиг:\n```yaml\ncache:\n  ttl: 30s\n  size: 10000\n```\n"+
		"> не забудьте `make migrate`\n\n@alice глянь ~town-square :+1:")
	img := s.seedPost(backend, bob, now.Add(-35*time.Minute), "Схема нового кэша")
	img.FileIds = model.StringArray{s.addFile(bob.Id, backend.Id, "схема.png", gradientPNG(640, 360)).Id}
	s.attachFiles(img)
	doc := s.seedPost(backend, dave, now.Add(-30*time.Minute), "Отчёт по нагрузке")
	doc.FileIds = model.StringArray{s.addFile(dave.Id, backend.Id, "load-report.pdf", []byte("%PDF-1.4\n% fake report\n")).Id}
	s.attachFiles(doc)
	// Ссылка с предпросмотром сайта (OpenGraph), как его добывает сервер.
	link := s.seedPost(backend, bob, now.Add(-28*time.Minute), "Почитайте про кэш: https://go.dev/blog/maps")
	link.Metadata = &model.PostMetadata{Embeds: []*model.PostEmbed{{Type: model.PostEmbedOpengraph, URL: "https://go.dev/blog/maps",
		Data: map[string]any{"title": "Go maps in action", "site_name": "The Go Programming Language",
			"description": "An introduction to Go maps: declaring, iterating, concurrency and ordering."}}}}
	video := s.seedPost(backend, carol, now.Add(-27*time.Minute), "Запись бага")
	video.FileIds = model.StringArray{s.addFile(carol.Id, backend.Id, "bug.avi", demoAVI(320, 180, 20, 10)).Id}
	s.attachFiles(video)
	spin := s.seedPost(backend, bob, now.Add(-25*time.Minute), "Деплой идёт")
	spin.FileIds = model.StringArray{s.addFile(bob.Id, backend.Id, "deploy.gif", spinnerGIF(160, 90)).Id}
	s.attachFiles(spin)
	// Вебхук Grafana, созданный bob: Mattermost пишет пост от имени bob, а имя
	// интеграции кладёт в props (на сервере разрешена подмена имени).
	alert := s.seedPost(alerts, bob, now.Add(-90*time.Minute), "")
	alert.AddProp(model.PostPropsFromWebhook, "true")
	alert.AddProp(model.PostPropsOverrideUsername, "Grafana")
	alert.AddProp(model.PostPropsAttachments, []*model.MessageAttachment{{
		Color: "danger", Title: "[FIRING:1] DiskSpaceLow", TitleLink: "https://grafana.example.com/alerting/1",
		Text: "Свободно **3%** на `/var/lib/postgresql`", Footer: "Grafana v11",
		Fields: []*model.MessageAttachmentField{{Title: "host", Value: "db-2", Short: true}, {Title: "severity", Value: "critical", Short: true}},
	}})
	s.markAllRead()

	// Тред в town-square: alice участвует, последний ответ dave она не видела.
	// Ответы не делают канал непрочитанным при CRT, поэтому без markAllRead.
	root := s.posts[town.Id][len(s.posts[town.Id])-3]
	s.seedReply(root, bob, now.Add(-50*time.Second), "Посмотрел, на staging то же самое.")
	s.seedReply(root, alice, now.Add(-40*time.Second), "Ок, заведу задачу.")
	s.seedReply(root, dave, now.Add(-30*time.Second), "@alice приложи туда логи nginx, пожалуйста")
	for _, r := range []struct {
		u    *model.User
		name string
	}{{bob, "+1"}, {alice, "+1"}, {carol, "tada"}, {dave, "partyparrot"}} {
		s.react(root, &model.Reaction{UserId: r.u.Id, PostId: root.Id, EmojiName: r.name, ChannelId: town.Id}, true)
	}

	// Непрочитанное у alice: в backend — просто новые, в личке с bob — упоминание.
	s.seedPost(backend, carol, now.Add(-20*time.Minute), "Задеплоил новую версию сервиса нотификаций.")
	s.seedPost(backend, dave, now.Add(-10*time.Minute), "Ловлю 502 на staging, кто-то трогал nginx?")
	s.seedPost(withBob, bob, now.Add(-5*time.Minute), "@alice глянь, пожалуйста, мой MR, когда будет минутка")
	s.members[alice.Id][withBob.Id].MentionCount = 1
	s.seedPost(alerts, bob, now.Add(-2*time.Hour), "[FIRING] DiskSpaceLow on db-2")
	s.members[alice.Id][alerts.Id].MsgCount = s.channels[alerts.Id].TotalMsgCount
}

// seedReply — ответ в тред при заполнении: счётчики и подписки как у живого.
func (s *Server) seedReply(root *model.Post, u *model.User, at time.Time, msg string) {
	p := &model.Post{Id: model.NewId(), ChannelId: root.ChannelId, RootId: root.Id, UserId: u.Id, Message: msg, CreateAt: at.UnixMilli(), UpdateAt: at.UnixMilli()}
	s.addPost(p)
	s.addReply(p, s.mentionedIn(p))
}

// mentionedIn — участники канала, упомянутые в посте через @username.
func (s *Server) mentionedIn(p *model.Post) []string {
	var out []string
	for id, u := range s.users {
		if id != p.UserId && s.members[id][p.ChannelId] != nil && strings.Contains(p.Message, "@"+u.Username) {
			out = append(out, id)
		}
	}
	return out
}

// UserID — id пользователя по имени, для тестов.
func (s *Server) UserID(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u := s.byName(name); u != nil {
		return u.Id
	}
	return ""
}

func (s *Server) addChannel(c *model.Channel, users []*model.User) {
	s.channels[c.Id] = c
	for _, u := range users {
		if s.members[u.Id] == nil {
			s.members[u.Id] = map[string]*model.ChannelMember{}
		}
		s.members[u.Id][c.Id] = &model.ChannelMember{ChannelId: c.Id, UserId: u.Id, NotifyProps: model.GetDefaultChannelNotifyProps()}
	}
}

func (s *Server) seedPost(c *model.Channel, u *model.User, at time.Time, msg string) *model.Post {
	p := &model.Post{Id: model.NewId(), ChannelId: c.Id, UserId: u.Id, Message: msg, CreateAt: at.UnixMilli(), UpdateAt: at.UnixMilli()}
	s.addPost(p)
	return p
}

func (s *Server) addPost(p *model.Post) {
	s.posts[p.ChannelId] = append(s.posts[p.ChannelId], p)
	c := s.channels[p.ChannelId]
	c.TotalMsgCount++
	if p.RootId == "" {
		c.TotalMsgCountRoot++
	}
	c.LastPostAt = max(c.LastPostAt, p.CreateAt)
	if m := s.members[p.UserId][p.ChannelId]; m != nil {
		m.MsgCount, m.MsgCountRoot, m.LastViewedAt = c.TotalMsgCount, c.TotalMsgCountRoot, p.CreateAt
	}
}

func (s *Server) markAllRead() {
	for _, byChannel := range s.members {
		for id, m := range byChannel {
			m.MsgCount, m.MsgCountRoot = s.channels[id].TotalMsgCount, s.channels[id].TotalMsgCountRoot
		}
	}
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

// myChannels — каналы команды, где me участник, плюс личные и групповые.
func (s *Server) myChannels(teamID string, me *model.User) []*model.Channel {
	var out []*model.Channel
	for id, c := range s.channels {
		if s.members[me.Id][id] != nil && (c.TeamId == teamID || c.IsGroupOrDirect()) {
			out = append(out, c)
		}
	}
	return out
}

func (s *Server) teamChannels(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.json(w, s.myChannels(r.PathValue("tid"), me))
}

func (s *Server) teamMembers(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []model.ChannelMember{}
	for _, c := range s.myChannels(r.PathValue("tid"), me) {
		out = append(out, *s.members[me.Id][c.Id])
	}
	s.json(w, out)
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

// channelPosts: page/per_page от самых новых; before=<post id> — страница старше этого поста.
func (s *Server) channelPosts(w http.ResponseWriter, r *http.Request, me *model.User) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	perPage, err := strconv.Atoi(q.Get("per_page"))
	if err != nil || perPage <= 0 {
		perPage = 60
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cid := r.PathValue("cid")
	if s.members[me.Id][cid] == nil {
		s.fail(w, http.StatusForbidden, "api.context.permissions.app_error")
		return
	}
	posts := s.posts[cid]
	if q.Get("collapsedThreads") == "true" {
		posts = slices.DeleteFunc(slices.Clone(posts), func(p *model.Post) bool { return p.RootId != "" })
	}
	end := len(posts)
	if before := q.Get("before"); before != "" {
		end = slices.IndexFunc(posts, func(p *model.Post) bool { return p.Id == before })
		if end < 0 {
			s.fail(w, http.StatusNotFound, "app.post.get.app_error")
			return
		}
	}
	end -= page * perPage
	list := model.NewPostList()
	for i := end - 1; i >= 0 && i >= end-perPage; i-- {
		list.AddPost(posts[i])
		list.AddOrder(posts[i].Id)
	}
	s.json(w, list)
}

func (s *Server) viewChannel(w http.ResponseWriter, r *http.Request, me *model.User) {
	var view model.ChannelView
	if err := json.NewDecoder(r.Body).Decode(&view); err != nil {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, c := s.members[me.Id][view.ChannelId], s.channels[view.ChannelId]
	if m == nil || c == nil {
		s.fail(w, http.StatusNotFound, "app.channel.get_member.missing.app_error")
		return
	}
	m.MsgCount, m.MsgCountRoot, m.MentionCount = c.TotalMsgCount, c.TotalMsgCountRoot, 0
	m.LastViewedAt = model.GetMillis()
	s.json(w, &model.ChannelViewResponse{Status: "OK", LastViewedAtTimes: map[string]int64{c.Id: m.LastViewedAt}})
}

func (s *Server) createPost(w http.ResponseWriter, r *http.Request, me *model.User) {
	var p model.Post
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	if s.members[me.Id][p.ChannelId] == nil {
		s.mu.Unlock()
		s.fail(w, http.StatusForbidden, "api.context.permissions.app_error")
		return
	}
	if p.RootId != "" && s.post(p.RootId) == nil {
		s.mu.Unlock()
		s.fail(w, http.StatusBadRequest, "api.post.create_post.root_id.app_error")
		return
	}
	p.UserId = me.Id
	for _, id := range p.FileIds {
		if f := s.files[id]; f == nil || f.info.CreatorId != me.Id || f.info.PostId != "" {
			s.mu.Unlock()
			s.fail(w, http.StatusBadRequest, "api.post.create_post.file_ids.app_error")
			return
		}
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
	s.json(w, s.Post(&p))
}

// Post публикует пост от имени p.UserId и рассылает posted по WebSocket.
func (s *Server) Post(p *model.Post) *model.Post {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Id = model.NewId()
	p.CreateAt = model.GetMillis()
	p.UpdateAt = p.CreateAt
	s.attachFiles(p)
	s.addPost(p)
	mentioned := s.mentionedIn(p)
	for _, id := range mentioned {
		m := s.members[id][p.ChannelId]
		m.MentionCount++
		if p.RootId == "" {
			m.MentionCountRoot++
		}
	}
	var followers []string
	if p.RootId != "" {
		followers = s.addReply(p, mentioned)
	}

	postJSON, _ := json.Marshal(p)
	for _, c := range s.connsIn(p.ChannelId) {
		ev := model.NewWebSocketEvent(model.WebsocketEventPosted, "", p.ChannelId, "", nil, "")
		ev.Add("post", string(postJSON))
		ev.Add("channel_type", string(s.channels[p.ChannelId].Type))
		if slices.Contains(mentioned, c.userID) {
			ev.Add("mentions", model.ArrayToJSON([]string{c.userID}))
		}
		if len(followers) > 0 {
			ev.Add("followers", model.ArrayToJSON(followers))
		}
		data, _ := ev.ToJSON()
		if err := c.write(data); err != nil {
			s.log.Warn("websocket write", "user", c.userID, "err", err)
		}
	}
	for _, id := range followers {
		s.sendThreadUpdated(p.RootId, id)
	}
	return p
}

// PostMissed публикует пост без события по WebSocket — как пост, о котором
// клиент не узнал, пока был без связи.
func (s *Server) PostMissed(p *model.Post) *model.Post {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Id = model.NewId()
	p.CreateAt = model.GetMillis()
	p.UpdateAt = p.CreateAt
	s.addPost(p)
	return p
}

// Chatter раз в interval пишет от bob в Town Square команды dev — для проверки realtime.
func (s *Server) Chatter(interval time.Duration) {
	s.mu.Lock()
	bob := s.byName("bob")
	var town *model.Channel
	for _, c := range s.channels {
		if c.Name == "town-square" && c.TeamId == s.teams[0].Id {
			town = c
		}
	}
	s.mu.Unlock()
	for i := 1; ; i++ {
		time.Sleep(interval)
		s.Post(&model.Post{ChannelId: town.Id, UserId: bob.Id, Message: fmt.Sprintf("Сообщение из chatter #%d", i)})
	}
}

// patchPost — автор меняет текст или свойства поста; убранный предпросмотр
// (remove_link_preview) сервер убирает и из метаданных. Всем в канале — post_edited.
func (s *Server) patchPost(w http.ResponseWriter, r *http.Request, me *model.User) {
	var patch model.PostPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.post(r.PathValue("pid"))
	if p == nil || p.UserId != me.Id {
		s.fail(w, http.StatusForbidden, "api.context.permissions.app_error")
		return
	}
	if patch.Message != nil {
		p.Message = *patch.Message
	}
	if patch.Props != nil {
		p.SetProps(*patch.Props)
	}
	if p.GetProp("remove_link_preview") == "true" && p.Metadata != nil {
		p.Metadata.Embeds = slices.DeleteFunc(p.Metadata.Embeds, func(e *model.PostEmbed) bool {
			return e.Type == model.PostEmbedOpengraph || e.Type == model.PostEmbedImage
		})
	}
	p.EditAt, p.UpdateAt = model.GetMillis(), model.GetMillis()
	postJSON, _ := json.Marshal(p)
	for _, c := range s.connsIn(p.ChannelId) {
		ev := model.NewWebSocketEvent(model.WebsocketEventPostEdited, "", p.ChannelId, "", nil, "")
		ev.Add("post", string(postJSON))
		data, _ := ev.ToJSON()
		c.write(data)
	}
	s.json(w, p)
}

// deletePost — автор удаляет пост; корень — вместе с ответами, как сервер.
// Всем в канале — post_deleted на каждый.
func (s *Server) deletePost(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.post(r.PathValue("pid"))
	if p == nil || p.UserId != me.Id {
		s.fail(w, http.StatusForbidden, "api.context.permissions.app_error")
		return
	}
	now := model.GetMillis()
	var gone []*model.Post
	s.posts[p.ChannelId] = slices.DeleteFunc(s.posts[p.ChannelId], func(q *model.Post) bool {
		if q.Id == p.Id || p.RootId == "" && q.RootId == p.Id {
			q.DeleteAt, q.UpdateAt = now, now
			gone = append(gone, q)
			return true
		}
		return false
	})
	if root := s.post(p.RootId); root != nil {
		root.ReplyCount--
	}
	for _, q := range gone {
		postJSON, _ := json.Marshal(q)
		for _, c := range s.connsIn(q.ChannelId) {
			ev := model.NewWebSocketEvent(model.WebsocketEventPostDeleted, "", q.ChannelId, "", nil, "")
			ev.Add("post", string(postJSON))
			data, _ := ev.ToJSON()
			c.write(data)
		}
	}
	s.json(w, map[string]string{"status": "OK"})
}

// setStatus — присутствие (и конец «не беспокоить»); всем — status_change.
func (s *Server) setStatus(w http.ResponseWriter, r *http.Request, me *model.User) {
	var st model.Status
	if err := json.NewDecoder(r.Body).Decode(&st); err != nil || st.Status == "" {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses[me.Id] = st.Status
	s.dndEnd[me.Id] = st.DNDEndTime
	ev := model.NewWebSocketEvent(model.WebsocketEventStatusChange, "", "", "", nil, "")
	ev.Add("user_id", me.Id)
	ev.Add("status", st.Status)
	s.broadcast(ev)
	st.UserId = me.Id
	s.json(w, &st)
}

// setCustomStatus — свой статус (PUT) или его сброс (DELETE); всем — user_updated.
func (s *Server) setCustomStatus(w http.ResponseWriter, r *http.Request, me *model.User) {
	var cs model.CustomStatus
	if r.Method == http.MethodPut {
		if err := json.NewDecoder(r.Body).Decode(&cs); err != nil {
			s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Method == http.MethodDelete {
		delete(me.Props, model.UserPropsKeyCustomStatus)
	} else {
		cs.PreSave()
		me.SetCustomStatus(&cs)
	}
	ev := model.NewWebSocketEvent(model.WebsocketEventUserUpdated, "", "", "", nil, "")
	ev.Add("user", me)
	s.broadcast(ev)
	s.json(w, map[string]string{"status": "OK"})
}

// broadcast — событие всем подключённым (под s.mu).
func (s *Server) broadcast(ev *model.WebSocketEvent) {
	data, _ := ev.ToJSON()
	for c := range s.conns {
		if err := c.write(data); err != nil {
			s.log.Warn("websocket write", "user", c.userID, "err", err)
		}
	}
}

func (s *Server) connsIn(channelID string) []*wsConn {
	var out []*wsConn
	for c := range s.conns {
		if s.members[c.userID][channelID] != nil {
			out = append(out, c)
		}
	}
	return out
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
	wc := &wsConn{userID: uid, c: conn}
	resp, _ := model.NewWebSocketResponse("OK", req.Seq, nil).ToJSON()
	wc.write(resp)
	hello := model.NewWebSocketEvent(model.WebsocketEventHello, "", "", uid, nil, "")
	hello.Add("server_version", "mmfake")
	data, _ := hello.ToJSON()
	wc.write(data)

	s.mu.Lock()
	s.conns[wc] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, wc)
		s.mu.Unlock()
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var req model.WebSocketRequest
			if err := conn.ReadJSON(&req); err != nil {
				return
			}
			if req.Action == "user_typing" {
				ch, _ := req.Data["channel_id"].(string)
				parent, _ := req.Data["parent_id"].(string)
				s.typing(uid, ch, parent)
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
			wc.mu.Lock()
			err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			wc.mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}
