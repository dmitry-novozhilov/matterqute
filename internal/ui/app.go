// Package ui — виджеты MIQT. Всё здесь — из главного потока Qt.
package ui

import (
	"context"
	"errors"
	"log/slog"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"matterqute/internal/config"
	"matterqute/internal/mm"
)

const requestTimeout = 30 * time.Second

// App решает, что показать: вход или главное окно активного сервера.
type App struct {
	cfg    *config.Config
	tokens *config.Tokens
	log    *slog.Logger

	login *LoginDialog
	main  *MainWindow
}

func NewApp(cfg *config.Config, tokens *config.Tokens, log *slog.Logger) *App {
	return &App{cfg: cfg, tokens: tokens, log: log}
}

func (a *App) Start() {
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
	l := NewLoginDialog(server, loginID)
	l.OnSubmit = a.submitLogin
	l.OnClosed = func() {
		// Сюда же приходим из closeLogin, но там a.login уже сброшен.
		if a.login == l {
			a.login = nil
			if a.main == nil {
				qt.QCoreApplication_Quit()
			}
		}
	}
	a.login = l
	l.Show()
}

// closeLogin закрывает окно входа; отложенные ответы сервера для него
// отбрасываются по a.login != l.
func (a *App) closeLogin() {
	if l := a.login; l != nil {
		a.login = nil
		l.Close()
	}
}

// resume входит по сохранённому токену; при неудаче оставляет окно входа.
func (a *App) resume(serverURL string) {
	token, err := a.tokens.Get(serverURL)
	if err != nil {
		if !errors.Is(err, config.ErrNoToken) {
			a.log.Error("read token", "server", serverURL, "err", err)
			a.login.SetError(err.Error(), false)
		}
		return
	}
	l := a.login
	l.OnRetry = func() { a.resume(serverURL) }
	l.SetBusy("Подключаемся к " + serverURL + "…")
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
				l.SetError("Сессия истекла, войдите заново.", false)
			default:
				a.log.Warn("resume session", "err", err)
				l.SetError(err.Error(), true)
			}
		})
	}()
}

func (a *App) submitLogin(f LoginForm) {
	l := a.login
	serverURL, err := mm.NormalizeServerURL(f.Server)
	if err != nil {
		l.SetError(err.Error(), false)
		return
	}
	if f.LoginID == "" || f.Password == "" {
		l.SetError("Введите логин и пароль.", false)
		return
	}
	l.SetBusy("Входим на " + serverURL + "…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		s, err := mm.Login(ctx, serverURL, f.LoginID, f.Password, f.MFA)
		mainthread.Start(func() {
			if a.login != l {
				return
			}
			switch {
			case errors.Is(err, mm.ErrMFARequired):
				l.AskMFA()
			case err != nil:
				a.log.Warn("login", "err", err)
				l.SetError(loginErrorText(err), false)
			default:
				a.remember(s, f.LoginID)
				a.openMain(s)
			}
		})
	}()
}

func loginErrorText(err error) string {
	var se *mm.ServerError
	if errors.As(err, &se) {
		switch se.ID {
		case "api.user.login.invalid_credentials_email_username",
			"api.user.login.invalid_credentials_email",
			"api.user.login.invalid_credentials_username",
			"api.user.login.invalid_credentials_sso":
			return "Неверный логин или пароль."
		case "api.user.check_user_mfa.bad_code.app_error":
			return "Неверный код MFA."
		}
	}
	return err.Error()
}

func (a *App) remember(s *mm.Session, loginID string) {
	if err := a.tokens.Set(s.ServerURL, s.Token()); err != nil {
		a.log.Error("save token", "err", err)
	}
	a.cfg.Upsert(config.Server{URL: s.ServerURL, LoginID: loginID, UserID: s.Me.Id, Username: s.Me.Username})
	if err := a.cfg.Save(); err != nil {
		a.log.Error("save config", "err", err)
	}
}

func (a *App) openMain(s *mm.Session) {
	a.log.Info("signed in", "server", s.ServerURL, "user", s.Me.Username)
	m := NewMainWindow(s, a.log)
	m.OnLogout = func() { a.logout(m) }
	a.main = m
	m.Show()
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
	// Окно входа — до закрытия главного, иначе Qt решит, что окон не осталось, и выйдет.
	a.main = nil
	a.showLogin(s.ServerURL, loginID)
	m.Close()
}

func (a *App) dropToken(serverURL string) {
	if err := a.tokens.Delete(serverURL); err != nil {
		a.log.Error("delete token", "err", err)
	}
}
