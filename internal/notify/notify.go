// Package notify — уведомления рабочего стола через org.freedesktop.Notifications
// (DBus). Qt не нужен: действие «Открыть» приходит в горутине DBus, и
// вызывающий сам переносит его в главный поток.
package notify

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"
)

const (
	dest  = "org.freedesktop.Notifications"
	path  = dbus.ObjectPath("/org/freedesktop/Notifications")
	iface = "org.freedesktop.Notifications"
)

// Notification — одно уведомление.
type Notification struct {
	Title    string
	Body     string // простой текст
	Image    string // картинка рядом с текстом (аватар автора), путь к файлу; "" — без неё
	Replaces uint32 // id показанного уведомления, которое заменить; 0 — новое
	// OnOpen — клик по уведомлению или «Открыть»; зовётся в горутине DBus.
	// token — XDG-токен активации от сервера уведомлений (на Wayland без него
	// окно не получит фокус); "" — сервер его не дал.
	OnOpen func(token string)
}

// Notifier шлёт уведомления; методы можно звать из любого потока.
type Notifier struct {
	conn   *dbus.Conn
	obj    dbus.BusObject
	app    string // имя приложения и его .desktop-файла
	icon   string // иконка приложения, путь к файлу
	markup bool   // сервер понимает разметку в тексте: <, > и & надо экранировать

	mu     sync.Mutex
	open   map[uint32]func(string)
	tokens map[uint32]string // ActivationToken приходит перед ActionInvoked
}

// New подключается к сессионной шине. Ошибка — уведомлений не будет: нет шины
// или некому их показывать.
func New(app, icon string) (*Notifier, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("notifications: connect to session bus: %w", err)
	}
	n := &Notifier{conn: conn, obj: conn.Object(dest, path), app: app, icon: icon,
		open: map[uint32]func(string){}, tokens: map[uint32]string{}}
	var caps []string
	if err := n.obj.Call(iface+".GetCapabilities", 0).Store(&caps); err != nil {
		conn.Close()
		return nil, fmt.Errorf("notifications: %s.GetCapabilities: %w", dest, err)
	}
	n.markup = slices.Contains(caps, "body-markup")
	if err := conn.AddMatchSignal(dbus.WithMatchObjectPath(path), dbus.WithMatchInterface(iface)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("notifications: subscribe to %s signals: %w", iface, err)
	}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	go n.listen(signals)
	return n, nil
}

var markupEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// Show показывает уведомление и возвращает его id (для Replaces).
func (n *Notifier) Show(x Notification) (uint32, error) {
	body := x.Body
	if n.markup {
		body = markupEscaper.Replace(body)
	}
	hints := map[string]dbus.Variant{
		"desktop-entry": dbus.MakeVariant(n.app),
		"category":      dbus.MakeVariant("im.received"),
		"sound-name":    dbus.MakeVariant("message-new-instant"),
	}
	if x.Image != "" {
		hints["image-path"] = dbus.MakeVariant((&url.URL{Scheme: "file", Path: x.Image}).String())
	}
	var actions []string
	if x.OnOpen != nil {
		// «default» — клик по самому уведомлению; где кнопки есть, подпись — «Открыть».
		actions = []string{"default", "Открыть"}
	}
	var id uint32
	err := n.obj.Call(iface+".Notify", 0, n.app, x.Replaces, n.icon, x.Title, body, actions, hints, int32(-1)).Store(&id)
	if err != nil {
		return 0, fmt.Errorf("notifications: Notify %q: %w", truncate(x.Title, 100), err)
	}
	n.mu.Lock()
	delete(n.open, x.Replaces)
	if x.OnOpen != nil {
		n.open[id] = x.OnOpen
	}
	n.mu.Unlock()
	return id, nil
}

func (n *Notifier) listen(signals <-chan *dbus.Signal) {
	for sig := range signals {
		if len(sig.Body) < 2 {
			continue
		}
		id, _ := sig.Body[0].(uint32)
		switch sig.Name {
		case iface + ".ActivationToken":
			if token, _ := sig.Body[1].(string); token != "" {
				n.mu.Lock()
				n.tokens[id] = token
				n.mu.Unlock()
			}
		case iface + ".ActionInvoked":
			n.mu.Lock()
			open, token := n.open[id], n.tokens[id]
			delete(n.tokens, id)
			n.mu.Unlock()
			if key, _ := sig.Body[1].(string); open != nil && key == "default" {
				open(token)
			}
		case iface + ".NotificationClosed":
			n.mu.Lock()
			delete(n.open, id)
			delete(n.tokens, id)
			n.mu.Unlock()
		}
	}
}

// Close отключается от шины; показанные уведомления остаются, но клик по ним
// уже ничего не откроет.
func (n *Notifier) Close() error { return n.conn.Close() }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
