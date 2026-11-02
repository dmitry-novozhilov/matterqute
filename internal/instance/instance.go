// Package instance — одно приложение на сессию: второй запуск (из меню, по
// ссылке mattermost:// из браузера) передаёт ссылку первому по DBus и
// выходит, а первый поднимает окно и открывает ссылку. Иначе было бы два
// окна с двумя подключениями и двойными уведомлениями.
package instance

import (
	"errors"
	"fmt"
	"sync"

	"github.com/godbus/dbus/v5"
)

const (
	busName = "io.github.matterqute"
	objPath = dbus.ObjectPath("/io/github/matterqute")
	iface   = "io.github.matterqute.App"
)

// ErrRunning — приложение уже запущено: ссылку передали ему.
var ErrRunning = errors.New("matterqute is already running")

// Instance — первое приложение: принимает ссылки от следующих запусков.
type Instance struct {
	conn *dbus.Conn

	mu      sync.Mutex
	handle  func(link, token string)
	pending [][2]string // пришли до SetHandler
}

// Claim — стать первым. Уже запущено — передать ему link и token (токен
// активации окна, XDG_ACTIVATION_TOKEN) и вернуть ErrRunning.
func Claim(link, token string) (*Instance, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("single instance: connect to session bus: %w", err)
	}
	in := &Instance{conn: conn}
	if err := conn.Export(in, objPath, iface); err != nil {
		conn.Close()
		return nil, fmt.Errorf("single instance: export %s: %w", objPath, err)
	}
	reply, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("single instance: request name %s: %w", busName, err)
	}
	if reply == dbus.RequestNameReplyPrimaryOwner {
		return in, nil
	}
	defer conn.Close()
	if err := conn.Object(busName, objPath).Call(iface+".Open", 0, link, token).Err; err != nil {
		return nil, fmt.Errorf("single instance: pass link to the running app: %w", err)
	}
	return nil, ErrRunning
}

// Open — метод DBus: ссылка от следующего запуска (пусто — просто поднять окно).
func (in *Instance) Open(link, token string) *dbus.Error {
	in.mu.Lock()
	h := in.handle
	if h == nil {
		in.pending = append(in.pending, [2]string{link, token})
	}
	in.mu.Unlock()
	if h != nil {
		h(link, token)
	}
	return nil
}

// SetHandler — кому отдавать ссылки; пришедшие раньше отдаются сразу. h
// зовётся из горутины DBus.
func (in *Instance) SetHandler(h func(link, token string)) {
	in.mu.Lock()
	in.handle = h
	pending := in.pending
	in.pending = nil
	in.mu.Unlock()
	for _, p := range pending {
		h(p[0], p[1])
	}
}

func (in *Instance) Close() error { return in.conn.Close() }
