package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/godbus/dbus/v5"
)

// Launcher — счётчик на значке приложения в доке: сигнал
// com.canonical.Unity.LauncherEntry.Update, его понимают доки KDE, Dash to
// Dock, Plank и другие. Док находит значок по имени .desktop-файла, поэтому
// без установленного matterqute.desktop (matterqute --install) счётчика не видно.
type Launcher struct {
	conn *dbus.Conn
	uri  string
	path dbus.ObjectPath
	last int64
}

const launcherIface = "com.canonical.Unity.LauncherEntry"

// NewLauncher — для приложения с файлом desktopID (например, «matterqute.desktop»).
func NewLauncher(desktopID string) (*Launcher, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("launcher badge: connect to session bus: %w", err)
	}
	sum := sha256.Sum256([]byte(desktopID))
	return &Launcher{conn: conn, uri: "application://" + desktopID, last: -1,
		path: dbus.ObjectPath("/com/canonical/unity/launcherentry/" + hex.EncodeToString(sum[:8]))}, nil
}

// SetCount — число на значке; 0 — убрать. Одинаковое повторно не шлём.
func (l *Launcher) SetCount(n int64) error {
	if n == l.last {
		return nil
	}
	l.last = n
	props := map[string]dbus.Variant{
		"count":         dbus.MakeVariant(n),
		"count-visible": dbus.MakeVariant(n > 0),
	}
	if err := l.conn.Emit(l.path, launcherIface+".Update", l.uri, props); err != nil {
		return fmt.Errorf("launcher badge: emit %s.Update: %w", launcherIface, err)
	}
	return nil
}

func (l *Launcher) Close() error { return l.conn.Close() }
