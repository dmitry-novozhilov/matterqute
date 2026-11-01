// Package ui — виджеты MIQT. Всё здесь — из главного потока Qt.
package ui

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/mappu/miqt/qt6/mainthread"

	"matterqute/internal/config"
	"matterqute/internal/mm"
)

const requestTimeout = 30 * time.Second

// App решает, что показать в окне: вход или окно активного сервера.
type App struct {
	cfg    *config.Config
	tokens *config.Tokens
	log    *slog.Logger

	shell *Shell
	login *LoginView
	main  *MainWindow
}

func NewApp(cfg *config.Config, tokens *config.Tokens, log *slog.Logger) *App {
	a := &App{cfg: cfg, tokens: tokens, log: log, shell: NewShell()}
	a.shell.OnLogout = func() {
		if a.main != nil {
			a.logout(a.main)
		}
	}
	OnThemeModeChosen = func(mode ThemeMode) {
		a.cfg.Theme = string(mode)
		if err := a.cfg.Save(); err != nil {
			a.log.Error("save config", "err", err)
		}
	}
	return a
}

func (a *App) Start() {
	a.shell.Show()
	srv := a.cfg.ActiveServer()
	if srv == nil {
		a.showLogin("", "")
		return
	}
	a.showLogin(srv.URL, srv.LoginID)
	a.resume(srv.URL)
}

func (a *App) showLogin(server, loginID string) {
	a.closeLogin()
	l := NewLoginView(server, loginID)
	l.OnSubmit = a.submitLogin
	a.login = l
	a.shell.SetPage(l.Widget())
	a.shell.SetUser("", "")
	a.shell.SetServer("")
	a.shell.SetConnection(ConnNone)
	l.Focus()
}

// closeLogin убирает страницу входа; отложенные ответы сервера для неё
// отбрасываются по a.login != l.
func (a *App) closeLogin() {
	if l := a.login; l != nil {
		a.login = nil
		l.Dispose()
		a.shell.RemovePage(l.Widget())
	}
}

// resume входит по сохранённому токену; при неудаче оставляет страницу входа.
func (a *App) resume(serverURL string) {
	token, err := a.tokens.Get(serverURL)
	if err != nil {
		if !errors.Is(err, config.ErrNoToken) {
			a.log.Error("read token", "server", serverURL, "err", err)
			a.login.SetError(err.Error())
		}
		return
	}
	l := a.login
	l.OnRetry = func() { a.resume(serverURL) }
	l.SetResuming(serverURL)
	a.shell.SetConnection(ConnConnecting)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		s, err := mm.Resume(ctx, serverURL, token)
		mainthread.Start(func() {
			if a.login != l {
				return
			}
			switch {
			case err == nil:
				a.openMain(s)
			case mm.IsUnauthorized(err):
				a.log.Info("saved session rejected", "server", serverURL, "err", err)
				a.dropToken(serverURL)
				a.shell.SetConnection(ConnNone)
				l.SetExpired()
			default:
				a.log.Warn("resume session", "err", err)
				a.shell.SetConnection(ConnUnavailable)
				l.SetUnavailable(err.Error(), true)
			}
		})
	}()
}

func (a *App) submitLogin(f LoginForm) {
	l := a.login
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
	a.shell.SetConnection(ConnConnecting)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		s, err := mm.Login(ctx, serverURL, f.LoginID, f.Password, f.MFA)
		mainthread.Start(func() {
			if a.login != l {
				return
			}
			var se *mm.ServerError
			switch {
			case errors.Is(err, mm.ErrMFARequired):
				a.shell.SetConnection(ConnNone)
				l.AskMFA()
			case err == nil:
				a.remember(s, f.LoginID)
				a.openMain(s)
			case errors.As(err, &se) && se.Status == 0:
				// Ответа не было вовсе: сеть, DNS, TLS.
				a.log.Warn("login", "err", err)
				a.shell.SetConnection(ConnUnavailable)
				l.SetUnavailable(err.Error(), false)
			default:
				a.log.Warn("login", "err", err)
				a.shell.SetConnection(ConnNone)
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
		a.log.Error("save token", "err", err)
	}
	srv := config.Server{URL: s.ServerURL, LoginID: loginID, UserID: s.Me.Id, Username: s.Me.Username}
	if old := a.cfg.Server(s.ServerURL); old != nil && old.UserID == s.Me.Id {
		srv.LastChannel = old.LastChannel
	}
	a.cfg.Upsert(srv)
	if err := a.cfg.Save(); err != nil {
		a.log.Error("save config", "err", err)
	}
}

func (a *App) openMain(s *mm.Session) {
	a.log.Info("signed in", "server", s.ServerURL, "user", s.Me.Username)
	lastChannel := ""
	if srv := a.cfg.Server(s.ServerURL); srv != nil {
		lastChannel = srv.LastChannel
	}
	cacheDir, err := config.CacheDir()
	if err != nil {
		a.log.Warn("cache dir, using temp", "err", err)
		cacheDir = filepath.Join(os.TempDir(), "matterqute-cache")
	}
	m := NewMainWindow(s, a.log, lastChannel, cacheDir)
	m.OnConnection = a.shell.SetConnection
	m.OnMessage = a.shell.ShowMessage
	m.OnChannelOpened = func(id string) {
		if srv := a.cfg.Server(s.ServerURL); srv != nil && srv.LastChannel != id {
			srv.LastChannel = id
			if err := a.cfg.Save(); err != nil {
				a.log.Error("save config", "err", err)
			}
		}
	}
	a.main = m
	a.shell.SetPage(m.Widget())
	a.shell.SetUser(s.Me.Username, s.ServerURL)
	a.shell.SetServer(s.ServerURL)
	a.shell.SetConnection(ConnConnecting)
	m.Start()
	a.closeLogin()
}

func (a *App) logout(m *MainWindow) {
	s := m.s
	a.dropToken(s.ServerURL)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		if err := s.Logout(ctx); err != nil {
			a.log.Warn("logout", "err", err)
		}
	}()
	loginID := ""
	if srv := a.cfg.Server(s.ServerURL); srv != nil {
		loginID = srv.LoginID
	}
	a.main = nil
	m.Close()
	a.shell.RemovePage(m.Widget())
	a.showLogin(s.ServerURL, loginID)
}

func (a *App) dropToken(serverURL string) {
	if err := a.tokens.Delete(serverURL); err != nil {
		a.log.Error("delete token", "err", err)
	}
}
