// Package mm — сетевой слой клиента: вход, REST (model.Client4) и realtime (WebSocket).
// О Qt ничего не знает; события отдаёт коллбеками из своих горутин.
package mm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// ErrMFARequired — сервер принял логин и пароль, но ждёт код MFA.
var ErrMFARequired = errors.New("mfa token required")

// Тот же признак, по которому переключается на ввод MFA веб-клиент
// (webapp/channels/src/components/login/login.tsx).
const mfaRequiredErrorID = "mfa.validate_token.authenticate.app_error"

// Session — авторизованное подключение к одному серверу.
type Session struct {
	ServerURL string
	Client    *model.Client4
	Me        *model.User
}

func (s *Session) Token() string { return s.Client.AuthToken }

// NormalizeServerURL приводит введённый адрес к виду https://host[/subpath] без завершающего слеша.
func NormalizeServerURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("server url is empty")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("bad server url %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("bad server url %q: scheme %q, want http or https", raw, u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("bad server url %q: no host", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

// Login входит по логину (email или username) и паролю. Пустой mfaToken —
// первая попытка; если серверу нужен код, вернётся ErrMFARequired.
func Login(ctx context.Context, serverURL, loginID, password, mfaToken string) (*Session, error) {
	c := model.NewAPIv4Client(serverURL)
	var (
		me   *model.User
		resp *model.Response
		err  error
	)
	if mfaToken == "" {
		me, resp, err = c.Login(ctx, loginID, password)
	} else {
		me, resp, err = c.LoginWithMFA(ctx, loginID, password, mfaToken)
	}
	if err != nil {
		var appErr *model.AppError
		if errors.As(err, &appErr) && appErr.Id == mfaRequiredErrorID {
			return nil, ErrMFARequired
		}
		return nil, describe(serverURL, "login", resp, err)
	}
	return &Session{ServerURL: serverURL, Client: c, Me: me}, nil
}

// Resume поднимает сессию по сохранённому токену.
func Resume(ctx context.Context, serverURL, token string) (*Session, error) {
	c := model.NewAPIv4Client(serverURL)
	c.SetToken(token)
	me, resp, err := c.GetMe(ctx, "")
	if err != nil {
		return nil, describe(serverURL, "resume session", resp, err)
	}
	return &Session{ServerURL: serverURL, Client: c, Me: me}, nil
}

func (s *Session) Logout(ctx context.Context) error {
	resp, err := s.Client.Logout(ctx)
	if err != nil {
		return describe(s.ServerURL, "logout", resp, err)
	}
	return nil
}

// IsUnauthorized — токен протух или отозван, нужен повторный вход.
func IsUnauthorized(err error) bool {
	var se *ServerError
	return errors.As(err, &se) && se.Status == http.StatusUnauthorized
}

// ServerError — ошибка запроса к серверу с тем, что нужно для разбора:
// адрес, операция, HTTP-статус и id ошибки Mattermost.
type ServerError struct {
	ServerURL string
	Op        string
	Status    int
	ID        string
	Err       error
}

func (e *ServerError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", e.Op, e.ServerURL)
	if e.Status != 0 {
		fmt.Fprintf(&b, ": HTTP %d", e.Status)
	}
	if e.ID != "" {
		fmt.Fprintf(&b, " (%s)", e.ID)
	}
	fmt.Fprintf(&b, ": %v", e.Err)
	return b.String()
}

func (e *ServerError) Unwrap() error { return e.Err }

func describe(serverURL, op string, resp *model.Response, err error) error {
	se := &ServerError{ServerURL: serverURL, Op: op, Err: err}
	if resp != nil {
		se.Status = resp.StatusCode
	}
	var appErr *model.AppError
	if errors.As(err, &appErr) {
		se.ID = appErr.Id
		if se.Status == 0 {
			se.Status = appErr.StatusCode
		}
	}
	return se
}
