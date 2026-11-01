// Package ui — виджеты MIQT. Всё здесь — из главного потока Qt.
package ui

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"matterqute/internal/config"
	"matterqute/internal/mm"
)

const requestTimeout = 30 * time.Second

// App решает, что показать в окне. Серверов может быть несколько: у каждого,
// куда вошли, своё окно, свой WebSocket, свои уведомления и счётчик; показан
// один, переключает меню «Сервер» (Ctrl+1…9).
type App struct {
	cfg    *config.Config
	tokens *config.Tokens
	log    *slog.Logger

	shell    *Shell
	tray     *Tray    // nil — трея нет
	notifier Notifier // nil — без уведомлений

	servers []*server  // в порядке cfg.Servers
	active  *server    // показанный сервер; nil — страница «Добавить сервер»
	adding  *LoginView // вход на новый сервер
}

// server — сервер из списка: окно, если вошли, иначе страница входа.
type server struct {
	url      string
	main     *MainWindow
	login    *LoginView
	conn     ConnState
	mentions int64
	mood     *qt.QPixmap // свой статус на этом сервере (эмодзи) и подсказка
	moodTip  string
}

// SetNotifier включает уведомления рабочего стола; звать до Start.
func (a *App) SetNotifier(n Notifier) { a.notifier = n }

func NewApp(cfg *config.Config, tokens *config.Tokens, log *slog.Logger) *App {
	a := &App{cfg: cfg, tokens: tokens, log: log, shell: NewShell()}
	a.shell.OnLogout = func() {
		if sv := a.active; sv != nil && sv.main != nil {
			a.logout(sv)
		}
	}
	a.shell.OnSelectServer = func(i int) { a.activate(a.servers[i]) }
	a.shell.OnAddServer = a.showAdd
	a.shell.OnStatus = func(at *qt.QPoint) {
		if sv := a.active; sv != nil && sv.main != nil {
			sv.main.showStatusPanel(at)
		}
	}
	OnThemeModeChosen = func(mode ThemeMode) {
		a.cfg.Theme = string(mode)
		a.saveConfig()
	}
	if a.tray = NewTray(); a.tray != nil {
		a.shell.HideOnClose = true
		a.tray.OnOpen = a.shell.Raise
		a.tray.OnQuit = a.shell.Quit
	}
	a.shell.OnClose = func() {
		a.cfg.Window.Geometry = a.shell.Geometry()
		if sv := a.active; sv != nil && sv.main != nil {
			a.cfg.Window.Sidebar, a.cfg.Window.Thread = sv.main.Columns()
		}
		a.saveConfig()
	}
	return a
}

func (a *App) saveConfig() {
	if err := a.cfg.Save(); err != nil {
		a.log.Error("save config", "err", err)
	}
}

// Start входит на все серверы с сохранёнными сессиями и показывает последний открытый.
func (a *App) Start() {
	a.shell.RestoreGeometry(a.cfg.Window.Geometry)
	a.shell.Show()
	for _, c := range a.cfg.Servers {
		a.servers = append(a.servers, &server{url: c.URL})
	}
	if len(a.servers) == 0 {
		a.showAdd()
		return
	}
	active := a.server(a.cfg.Active)
	if active == nil {
		active = a.servers[0]
	}
	a.active = active
	for _, sv := range a.servers {
		a.ensureLogin(sv)
		a.resume(sv)
	}
	a.activate(active)
}

func (a *App) server(url string) *server {
	for _, sv := range a.servers {
		if sv.url == url {
			return sv
		}
	}
	return nil
}

// activate показывает сервер: его окно или страницу входа.
func (a *App) activate(sv *server) {
	a.closeAdding()
	a.active = sv
	if a.cfg.Active != sv.url {
		a.cfg.Active = sv.url
		a.saveConfig()
	}
	if sv.main != nil {
		// Ширины колонок общие: подтягиваем те, что выставили на другом сервере.
		sv.main.SetColumns(a.cfg.Window.Sidebar, a.cfg.Window.Thread)
		a.shell.SetPage(sv.main.Widget())
		a.shell.SetUser(sv.main.s.Me.Username, sv.url)
		a.shell.SetMood(sv.mood, sv.moodTip)
	} else {
		a.shell.SetPage(sv.login.Widget())
		a.shell.SetUser("", "")
		sv.login.Focus()
	}
	a.shell.SetConnection(sv.conn)
	a.updateServers()
}

// showAdd — «Добавить сервер…»: пустая страница входа. Уйти с неё — выбрать
// сервер в меню.
func (a *App) showAdd() {
	if a.adding == nil {
		l := NewLoginView("", "")
		l.OnSubmit = func(f LoginForm) { a.submitLogin(l, f) }
		a.adding = l
	}
	a.active = nil
	a.shell.SetPage(a.adding.Widget())
	a.shell.SetUser("", "")
	a.shell.SetConnection(ConnNone)
	a.updateServers()
	a.adding.Focus()
}

func (a *App) closeAdding() {
	if l := a.adding; l != nil {
		a.adding = nil
		l.Dispose()
		a.shell.RemovePage(l.Widget())
	}
}

// ensureLogin заводит страницу входа сервера: адрес и логин — из настроек.
func (a *App) ensureLogin(sv *server) {
	if sv.login != nil {
		return
	}
	loginID := ""
	if c := a.cfg.Server(sv.url); c != nil {
		loginID = c.LoginID
	}
	l := NewLoginView(sv.url, loginID)
	l.OnSubmit = func(f LoginForm) { a.submitLogin(l, f) }
	sv.login = l
}

func (a *App) dropLogin(sv *server) {
	if l := sv.login; l != nil {
		sv.login = nil
		l.Dispose()
		a.shell.RemovePage(l.Widget())
	}
}

// loginAlive — страница входа ещё на месте; ответы сервера для убранной
// страницы отбрасываются.
func (a *App) loginAlive(l *LoginView) bool {
	if l == a.adding {
		return true
	}
	for _, sv := range a.servers {
		if sv.login == l {
			return true
		}
	}
	return false
}

// setConn — состояние соединения сервера; в строке состояния — только у показанного.
func (a *App) setConn(sv *server, c ConnState) {
	sv.conn = c
	if sv == a.active {
		a.shell.SetConnection(c)
	}
}

// loginConn — состояние входа со страницы l; в строке состояния — если она на экране.
func (a *App) loginConn(l *LoginView, c ConnState) {
	for _, sv := range a.servers {
		if sv.login == l {
			a.setConn(sv, c)
			return
		}
	}
	if l == a.adding && a.active == nil {
		a.shell.SetConnection(c)
	}
}

// resume входит по сохранённому токену; при неудаче остаётся страница входа.
func (a *App) resume(sv *server) {
	token, err := a.tokens.Get(sv.url)
	if err != nil {
		if !errors.Is(err, config.ErrNoToken) {
			a.log.Error("read token", "server", sv.url, "err", err)
			sv.login.SetError(err.Error())
		}
		return
	}
	l := sv.login
	l.OnRetry = func() { a.resume(sv) }
	l.SetResuming(sv.url)
	a.setConn(sv, ConnConnecting)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		s, err := mm.Resume(ctx, sv.url, token)
		mainthread.Start(func() {
			if sv.login != l {
				return
			}
			switch {
			case err == nil:
				a.openMain(sv, s, false)
			case mm.IsUnauthorized(err):
				a.log.Info("saved session rejected", "server", sv.url, "err", err)
				a.dropToken(sv.url)
				a.setConn(sv, ConnNone)
				l.SetExpired()
			default:
				a.log.Warn("resume session", "server", sv.url, "err", err)
				a.setConn(sv, ConnUnavailable)
				l.SetUnavailable(err.Error(), true)
			}
		})
	}()
}

func (a *App) submitLogin(l *LoginView, f LoginForm) {
	serverURL, err := mm.NormalizeServerURL(f.Server)
	if err != nil {
		l.SetError(err.Error())
		return
	}
	if f.LoginID == "" || f.Password == "" {
		l.SetFieldError("Введите логин и пароль.")
		return
	}
	l.SetBusy()
	a.loginConn(l, ConnConnecting)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		s, err := mm.Login(ctx, serverURL, f.LoginID, f.Password, f.MFA)
		mainthread.Start(func() {
			if !a.loginAlive(l) {
				return
			}
			var se *mm.ServerError
			switch {
			case errors.Is(err, mm.ErrMFARequired):
				a.loginConn(l, ConnNone)
				l.AskMFA()
			case err == nil:
				a.remember(s, f.LoginID)
				sv := a.server(s.ServerURL)
				if sv == nil { // новый сервер: в конец списка, как и в настройках
					sv = &server{url: s.ServerURL}
					a.servers = append(a.servers, sv)
				}
				if sv.main != nil { // вошли заново туда, где уже были
					a.closeMain(sv)
				}
				a.openMain(sv, s, true)
			case errors.As(err, &se) && se.Status == 0:
				// Ответа не было вовсе: сеть, DNS, TLS.
				a.log.Warn("login", "server", serverURL, "err", err)
				a.loginConn(l, ConnUnavailable)
				l.SetUnavailable(err.Error(), false)
			default:
				a.log.Warn("login", "server", serverURL, "err", err)
				a.loginConn(l, ConnNone)
				if msg, ok := credentialsError(err); ok {
					l.SetFieldError(msg)
				} else {
					l.SetError(err.Error())
				}
			}
		})
	}()
}

// credentialsError — понятный текст для неверных логина, пароля или кода.
func credentialsError(err error) (string, bool) {
	var se *mm.ServerError
	if errors.As(err, &se) {
		switch se.ID {
		case "api.user.login.invalid_credentials_email_username",
			"api.user.login.invalid_credentials_email",
			"api.user.login.invalid_credentials_username",
			"api.user.login.invalid_credentials_sso":
			return "Неверный логин или пароль.", true
		case "api.user.check_user_mfa.bad_code.app_error":
			return "Неверный код. Проверьте время на телефоне и введите новый код.", true
		}
	}
	return "", false
}

func (a *App) remember(s *mm.Session, loginID string) {
	if err := a.tokens.Set(s.ServerURL, s.Token()); err != nil {
		a.log.Error("save token", "server", s.ServerURL, "err", err)
	}
	srv := config.Server{URL: s.ServerURL, LoginID: loginID, UserID: s.Me.Id, Username: s.Me.Username}
	if old := a.cfg.Server(s.ServerURL); old != nil && old.UserID == s.Me.Id {
		srv.LastChannel = old.LastChannel
	}
	a.cfg.Upsert(srv)
	a.saveConfig()
}

// openMain — окно сервера вместо страницы входа. show — показать его сразу
// (вошли руками); после фонового входа по токену показываем, только если
// этот сервер и так на экране.
func (a *App) openMain(sv *server, s *mm.Session, show bool) {
	a.log.Info("signed in", "server", s.ServerURL, "user", s.Me.Username)
	lastChannel := ""
	if c := a.cfg.Server(s.ServerURL); c != nil {
		lastChannel = c.LastChannel
	}
	cacheDir, err := config.CacheDir()
	if err != nil {
		a.log.Warn("cache dir, using temp", "err", err)
		cacheDir = filepath.Join(os.TempDir(), "matterqute-cache")
	}
	m := NewMainWindow(s, a.log, lastChannel, cacheDir)
	m.SetColumns(a.cfg.Window.Sidebar, a.cfg.Window.Thread)
	m.OnColumnsChanged = func(sidebar, thread int) {
		a.cfg.Window.Sidebar, a.cfg.Window.Thread = sidebar, thread
		a.saveConfig()
	}
	m.notifier = a.notifier
	m.OnRaise = func() {
		a.activate(sv) // уведомление могло прийти с другого сервера
		a.shell.Raise()
	}
	m.OnMentions = func(n int64) {
		sv.mentions = n
		a.updateMentions()
	}
	m.OnConnection = func(c ConnState) { a.setConn(sv, c) }
	m.OnOwnStatus = func(pm *qt.QPixmap, tip string) {
		sv.mood, sv.moodTip = pm, tip
		if sv == a.active {
			a.shell.SetMood(pm, tip)
		}
	}
	m.OnMessage = func(text string) {
		if sv == a.active {
			a.shell.ShowMessage(text)
		}
	}
	m.OnChannelOpened = func(id string) {
		if c := a.cfg.Server(s.ServerURL); c != nil && c.LastChannel != id {
			c.LastChannel = id
			a.saveConfig()
		}
	}
	sv.main = m
	sv.conn = ConnConnecting
	m.Start()
	if show || sv == a.active {
		a.activate(sv)
	} else {
		a.updateServers()
	}
	a.dropLogin(sv)
}

func (a *App) closeMain(sv *server) {
	m := sv.main
	sv.main, sv.mentions, sv.conn = nil, 0, ConnNone
	m.Close()
	a.shell.RemovePage(m.Widget())
	a.updateMentions()
}

// logout — выйти с сервера: сессия закрывается, сервер остаётся в списке со
// страницей входа.
func (a *App) logout(sv *server) {
	s := sv.main.s
	a.dropToken(sv.url)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		if err := s.Logout(ctx); err != nil {
			a.log.Warn("logout", "server", sv.url, "err", err)
		}
	}()
	a.ensureLogin(sv)
	a.closeMain(sv)
	if sv == a.active {
		a.activate(sv)
	}
}

func (a *App) dropToken(serverURL string) {
	if err := a.tokens.Delete(serverURL); err != nil {
		a.log.Error("delete token", "server", serverURL, "err", err)
	}
}

// updateMentions — счётчик трея: упоминания и непрочитанные лички всех серверов.
func (a *App) updateMentions() {
	var total int64
	for _, sv := range a.servers {
		total += sv.mentions
	}
	if a.tray != nil {
		a.tray.SetMentions(total)
	}
	a.updateServers()
}

// updateServers — меню «Сервер»: текущий с галочкой, у остальных — счётчик.
func (a *App) updateServers() {
	items := make([]ServerItem, len(a.servers))
	for i, sv := range a.servers {
		items[i] = ServerItem{Host: hostOf(sv.url), Active: sv == a.active, Mentions: sv.mentions}
	}
	a.shell.SetServers(items)
}
