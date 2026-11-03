// Package ui — виджеты MIQT. Всё здесь — из главного потока Qt.
package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"matterqute/internal/config"
	"matterqute/internal/mm"
)

const (
	requestTimeout = 30 * time.Second
	// Повтор загрузки сайдбара после ошибки: от 5 с, удваивая до минуты.
	sidebarRetryMin = 5 * time.Second
	sidebarRetryMax = time.Minute
)

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
	launcher Badge    // nil — без счётчика на значке в доке

	servers []*server  // в порядке cfg.Servers
	active  *server    // показанный сервер; nil — страница «Добавить сервер»
	adding  *LoginView // вход на новый сервер

	removeDialog *dialog // последнее окно «Удалить сервер» (для тестов)
	quitSaved    bool    // при выходе всё уже сохранено
}

// server — сервер из списка: окно, если вошли, иначе страница входа.
type server struct {
	url      string
	main     *MainWindow
	login    *LoginView
	conn     ConnState
	counts   Counts
	mood     *qt.QPixmap // свой статус на этом сервере: эмодзи, текст, подсказка
	moodText string
	moodTip  string
	pending  string // путь из ссылки снаружи: открыть, когда войдём
}

// SetNotifier включает уведомления рабочего стола; звать до Start.
func (a *App) SetNotifier(n Notifier) { a.notifier = n }

// Badge — счётчик на значке в доке (notify.Launcher).
type Badge interface{ SetCount(n int64) error }

// SetLauncher включает счётчик на значке в доке; звать до Start.
func (a *App) SetLauncher(b Badge) { a.launcher = b }

func NewApp(cfg *config.Config, tokens *config.Tokens, log *slog.Logger) *App {
	a := &App{cfg: cfg, tokens: tokens, log: log, shell: NewShell()}
	a.shell.OnLogout = func() {
		if sv := a.active; sv != nil && sv.main != nil {
			a.logout(sv)
		}
	}
	a.shell.OnSelectServer = func(i int) { a.activate(a.servers[i]) }
	a.shell.OnAddServer = a.showAdd
	a.shell.OnRemoveServer = func() {
		if sv := a.active; sv != nil {
			a.confirmRemove(sv)
		}
	}
	a.shell.OnStatus = func(at *qt.QPoint) {
		if sv := a.active; sv != nil && sv.main != nil {
			sv.main.showStatusPanel(at)
		}
	}
	OnThemeModeChosen = func(mode ThemeMode) {
		a.cfg.Theme = string(mode)
		a.saveConfig()
	}
	OnFontsChosen = func(f config.Fonts) {
		a.cfg.Fonts = f
		a.saveConfig()
	}
	OnZoomChosen = func(percent int) {
		a.cfg.Zoom = percent
		if percent == 100 {
			a.cfg.Zoom = 0
		}
		a.saveConfig()
		a.shell.ShowMessage(fmt.Sprintf("Масштаб: %d%%", percent))
	}
	OnFormatBarChosen = func(on bool) {
		a.cfg.FormatBar = on
		a.saveConfig()
	}
	a.shell.OnSwitcher = func() {
		if sv := a.active; sv != nil && sv.main != nil {
			sv.main.showSwitcher()
		}
	}
	a.shell.OnNewMessage = func() {
		if sv := a.active; sv != nil && sv.main != nil {
			sv.main.showNewMessage()
		}
	}
	a.shell.OnProfile = func() {
		if sv := a.active; sv != nil && sv.main != nil {
			sv.main.showProfile()
		}
	}
	a.shell.OnNameFormatState = func() (string, bool, bool) {
		if sv := a.active; sv != nil && sv.main != nil {
			f, locked := sv.main.st.NameFormat()
			return f, locked, true
		}
		return "", false, false
	}
	a.shell.OnNameFormat = func(format string) {
		if sv := a.active; sv != nil && sv.main != nil {
			sv.main.setNameFormat(format)
		}
	}
	a.shell.OnMarkAllRead = func() {
		if sv := a.active; sv != nil && sv.main != nil {
			sv.main.markAllRead()
		}
	}
	a.shell.OnBrowseChannels = func() {
		if sv := a.active; sv != nil && sv.main != nil {
			sv.main.showBrowseChannels("")
		}
	}
	a.shell.OnCreateChannel = func() {
		if sv := a.active; sv != nil && sv.main != nil {
			sv.main.showCreateChannel("")
		}
	}
	OnTypingChosen = func(show, send bool) {
		a.cfg.Typing = config.Typing{Hide: !show, NoSend: !send}
		a.saveConfig()
	}
	OnSpellcheckChosen = func(on bool) {
		a.cfg.NoSpellcheck = !on
		a.saveConfig()
	}
	OnTaskbarAlertChosen = func(on bool) {
		a.cfg.NoTaskbarAlert = !on
		a.saveConfig()
	}
	if a.tray = NewTray(a.cfg.Tray); a.tray != nil {
		a.shell.HideOnClose = true
		a.tray.OnOpen = a.shell.Raise
		a.tray.OnQuit = a.shell.Quit
		a.shell.trayAct.SetVisible(true)
		a.shell.OnTrayColors = func() {
			showTrayDialog(a.shell.w.QWidget, a.cfg.Tray, func(c config.Tray) {
				a.cfg.Tray = c
				a.saveConfig()
				a.tray.Configure(c)
			})
		}
	}
	a.shell.OnClose = func() {
		a.cfg.Window.Geometry = a.shell.Geometry()
		if sv := a.active; sv != nil && sv.main != nil {
			a.cfg.Window.Sidebar, a.cfg.Window.Thread = sv.main.Columns()
		}
		a.saveConfig()
	}
	// Окна серверов при выходе не закрываются (процесс просто завершается):
	// черновики и снимки для быстрого запуска — здесь. «Выход» — OnQuit,
	// последнее окно закрыли без трея — aboutToQuit (подключает main).
	a.shell.OnQuit = a.BeforeQuit
	return a
}

// BeforeQuit — приложение завершается: сохранить черновики и снимки серверов (один раз).
func (a *App) BeforeQuit() {
	if a.quitSaved {
		return
	}
	a.quitSaved = true
	for _, sv := range a.servers {
		if m := sv.main; m != nil && !m.closed {
			m.drafts.save(true)
			m.saveState(true)
		}
	}
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
		a.shell.SetMood(sv.mood, sv.moodText, sv.moodTip)
	} else {
		a.shell.SetPage(sv.login.Widget())
		a.shell.SetUser("", "")
		sv.login.Focus()
	}
	a.shell.SetConnection(sv.conn)
	a.shell.browseAct.SetEnabled(sv.main != nil)
	a.shell.createAct.SetEnabled(sv.main != nil)
	a.shell.switchAct.SetEnabled(sv.main != nil)
	a.shell.newMsgAct.SetEnabled(sv.main != nil)
	a.shell.readAllAct.SetEnabled(sv.main != nil)
	a.shell.profileAct.SetEnabled(sv.main != nil)
	a.updateServers()
}

// OpenLink — ссылка снаружи (mattermost:// из браузера, адрес сервера
// аргументом запуска): окно наверх, сервер ссылки — на экран, путь — в нём.
// Сервера нет в списке — страница добавления с его адресом. token — токен
// активации окна (Wayland) от того, кто передал ссылку.
func (a *App) OpenLink(link, token string) {
	if token != "" {
		os.Setenv("XDG_ACTIVATION_TOKEN", token) // Qt возьмёт его, поднимая окно
	}
	a.shell.Raise()
	if link == "" {
		return
	}
	// Сначала окно: пусть оконный менеджер покажет и отрисует его, переход —
	// после (загрузка ленты и треда занимает главный поток, и поднятое окно
	// стояло пустым, с одним заголовком).
	t := qt.NewQTimer2(a.shell.w.QObject)
	t.SetSingleShot(true)
	t.OnTimeout(func() {
		t.DeleteLater()
		a.openLink(link)
	})
	t.Start(linkDelay)
}

// linkDelay — пауза между подъёмом окна и переходом по ссылке, мс.
const linkDelay = 150

func (a *App) openLink(link string) {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" || (u.Scheme != "mattermost" && u.Scheme != "http" && u.Scheme != "https") {
		a.log.Warn("open link", "link", truncate(link, 200), "err", err)
		a.shell.ShowMessage("Не понял ссылку: " + truncate(link, 200))
		return
	}
	sv, path := a.serverOf(u)
	if sv == nil {
		a.showAdd()
		a.adding.server.SetText(u.Host)
		a.shell.ShowMessage("Сервер " + u.Host + " не добавлен: войдите, и ссылку можно будет открыть")
		return
	}
	a.activate(sv)
	if sv.main != nil {
		sv.main.OpenPath(path)
	} else {
		sv.pending = path
	}
}

// openOnOtherServer — ссылка из сообщения на другой добавленный сервер.
func (a *App) openOnOtherServer(link string) bool {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" {
		return false
	}
	sv, path := a.serverOf(u)
	if sv == nil || sv == a.active || sv.main == nil {
		return false
	}
	a.activate(sv)
	sv.main.OpenPath(path)
	return true
}

// serverOf — сервер из списка по адресу ссылки и путь на нём.
func (a *App) serverOf(u *url.URL) (*server, string) {
	for _, sv := range a.servers {
		own, err := url.Parse(sv.url)
		if err != nil || !strings.EqualFold(own.Host, u.Host) {
			continue
		}
		base := strings.TrimSuffix(own.Path, "/")
		if base == "" || u.Path == base || strings.HasPrefix(u.Path, base+"/") {
			return sv, strings.TrimPrefix(u.Path, base)
		}
	}
	return nil, ""
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
	a.shell.browseAct.SetEnabled(false)
	a.shell.createAct.SetEnabled(false)
	a.shell.switchAct.SetEnabled(false)
	a.shell.newMsgAct.SetEnabled(false)
	a.shell.readAllAct.SetEnabled(false)
	a.shell.profileAct.SetEnabled(false)
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
	path := statePath(a.cacheBase(), sv.url)
	go func() {
		// Есть снимок — окно из него сразу, токен проверяем следом (state.go).
		st, err := readState(path)
		if err != nil {
			a.log.Warn("saved state", "server", sv.url, "err", err)
		}
		if st != nil {
			mainthread.Start(func() {
				if sv.login == l {
					a.openMain(sv, mm.Offline(sv.url, token, st.Me), false, st)
					a.checkToken(sv, token, st.Me.Id)
				}
			})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		s, err := mm.Resume(ctx, sv.url, token)
		mainthread.Start(func() {
			if sv.login != l {
				return
			}
			switch {
			case err == nil:
				a.openMain(sv, s, false, nil)
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
				a.openMain(sv, s, true, nil)
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
func (a *App) openMain(sv *server, s *mm.Session, show bool, st *savedState) {
	a.log.Info("signed in", "server", s.ServerURL, "user", s.Me.Username, "from_saved_state", st != nil)
	lastChannel := ""
	if c := a.cfg.Server(s.ServerURL); c != nil {
		lastChannel = c.LastChannel
	}
	m := NewMainWindow(s, a.log, lastChannel, a.cacheBase())
	m.restore = st
	if path := a.cfg.ScheduledPath(s.ServerURL); path != "" {
		m.sched.path = path // отложенные, которые отправляет приложение, — с настройками, не в кэше
	}
	if path := a.cfg.DraftsPath(s.ServerURL); path != "" {
		m.drafts.path = path // если черновики не хранит сервер — с настройками, не в кэше
	}
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
	m.OnCounts = func(c Counts) {
		sv.counts = c
		a.updateCounts()
	}
	m.OnConnection = func(c ConnState) { a.setConn(sv, c) }
	m.OnOtherServerLink = a.openOnOtherServer
	if sv.pending != "" {
		m.OpenPath(sv.pending)
		sv.pending = ""
	}
	m.OnOwnStatus = func(pm *qt.QPixmap, text, tip string) {
		sv.mood, sv.moodText, sv.moodTip = pm, text, tip
		if sv == a.active {
			a.shell.SetMood(pm, text, tip)
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
	// Окно сервера — на экран до Start: из снимка лента строится в Start, и
	// виджету лучше уже стоять на своём месте (см. Feed.checkLayout).
	shown := show || sv == a.active
	if shown {
		a.activate(sv)
	}
	m.Start()
	if !shown {
		a.updateServers()
	}
	a.dropLogin(sv)
}

// cacheBase — общий каталог кэша (в нём у каждого сервера свой).
func (a *App) cacheBase() string {
	dir, err := config.CacheDir()
	if err != nil {
		a.log.Warn("cache dir, using temp", "err", err)
		return filepath.Join(os.TempDir(), "matterqute-cache")
	}
	return dir
}

// checkToken — окно открыто из снимка: проверить токен. Тот же пользователь —
// свежий профиль в окно; другой (снимок от прошлого входа) — окно заново,
// без снимка; токен отозван — страница входа, снимок прочь; сервер
// недоступен — остаётся снимок, связь переподключится сама.
func (a *App) checkToken(sv *server, token, userID string) {
	m := sv.main
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		s, err := mm.Resume(ctx, sv.url, token)
		mainthread.Start(func() {
			if sv.main != m {
				return // окно уже закрыли (выход, удаление сервера)
			}
			switch {
			case err == nil && s.Me.Id == userID:
				m.setMe(s.Me)
			case err == nil:
				a.log.Info("saved state of another user, reopening", "server", sv.url)
				m.dropState()
				a.closeMain(sv)
				a.openMain(sv, s, a.active == sv, nil)
			case mm.IsUnauthorized(err):
				a.log.Info("saved session rejected", "server", sv.url, "err", err)
				m.dropState()
				a.dropToken(sv.url)
				a.ensureLogin(sv)
				a.closeMain(sv)
				sv.login.SetExpired()
				if sv == a.active {
					a.activate(sv)
				}
			default:
				a.log.Warn("check saved session", "server", sv.url, "err", err)
			}
		})
	}()
}

func (a *App) closeMain(sv *server) {
	m := sv.main
	sv.main, sv.counts, sv.conn = nil, Counts{}, ConnNone
	m.Close()
	a.shell.RemovePage(m.Widget())
	a.updateCounts()
}

// logout — выйти с сервера: сессия закрывается, сервер остаётся в списке со
// страницей входа.
func (a *App) logout(sv *server) {
	s := sv.main.s
	a.dropToken(sv.url)
	sv.main.dropState() // в снимке переписка
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

// updateCounts — трей и док: сумма по всем серверам. В доке — только
// сообщения мне (упоминания и лички).
func (a *App) updateCounts() {
	var total Counts
	for _, sv := range a.servers {
		total = total.add(sv.counts)
	}
	if a.tray != nil {
		a.tray.SetCounts(total)
	}
	if a.launcher != nil {
		if err := a.launcher.SetCount(total.Mentions); err != nil {
			a.log.Warn("dock badge", "err", err)
		}
	}
	a.updateServers()
}

// updateServers — меню «Сервер»: текущий с галочкой, у остальных — счётчик.
func (a *App) updateServers() {
	items := make([]ServerItem, len(a.servers))
	for i, sv := range a.servers {
		items[i] = ServerItem{Host: hostOf(sv.url), Active: sv == a.active, Mentions: sv.counts.Mentions}
	}
	a.shell.SetServers(items)
}

// confirmRemove — «Сервер → Удалить сервер…»: спросить и удалить.
func (a *App) confirmRemove(sv *server) {
	d := newDialog(a.shell.w.QWidget, "Удалить сервер")
	a.removeDialog = d
	text := qt.NewQLabel3("Удалить сервер " + hostOf(sv.url) + " из списка? Сессия на нём будет закрыта, черновики пропадут.")
	text.SetWordWrap(true)
	d.lay.AddWidget(text.QWidget)
	danger(d.buttons("Удалить", func() { a.removeServer(sv) }))
	d.w.Show()
}

// removeServer — выйти с сервера и убрать его из списка и настроек; на экране —
// следующий сервер или страница добавления.
func (a *App) removeServer(sv *server) {
	if sv.main != nil {
		s := sv.main.s
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
			defer cancel()
			if err := s.Logout(ctx); err != nil {
				a.log.Warn("logout", "server", sv.url, "err", err)
			}
		}()
		sv.main.dropState()
		a.closeMain(sv)
	} else if err := os.Remove(statePath(a.cacheBase(), sv.url)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		a.log.Warn("drop saved state", "server", sv.url, "err", err)
	}
	a.dropToken(sv.url)
	a.dropLogin(sv)
	a.servers = slices.DeleteFunc(a.servers, func(o *server) bool { return o == sv })
	a.cfg.Remove(sv.url)
	a.saveConfig()
	a.log.Info("server removed", "server", sv.url)
	a.updateCounts()
	if len(a.servers) == 0 {
		a.showAdd()
		return
	}
	if a.active == sv {
		next := a.server(a.cfg.Active)
		if next == nil {
			next = a.servers[0]
		}
		a.activate(next)
	}
}
