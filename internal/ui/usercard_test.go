package ui

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

func TestUserCardOfflineDark(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}

		// Карточка по клику на @carol: имя, статус с сервера, «Написать» открывает личку.
		m.handleLink("mm-user:carol")
		var card *qt.QWidget
		if !waitFor(t, "user card", func() bool { card = qt.QApplication_ActivePopupWidget(); return card != nil }) {
			return
		}
		label := func(name string) string {
			for _, o := range descendants(card.QObject) {
				if o.ObjectName() == name {
					return qt.UnsafeNewQLabel(o.UnsafePointer()).Text()
				}
			}
			return ""
		}
		if got := label("userName"); got != "Карина Орлова" {
			t.Errorf("card name %q, want Карина Орлова", got)
		}
		if got := label("userRole"); got != "Тимлид" {
			t.Errorf("card position %q, want Тимлид", got)
		}
		shotWidget(t, card, "user-card")
		var write *qt.QPushButton
		for _, o := range descendants(card.QObject) {
			if pb := qt.UnsafeNewQPushButton(o.UnsafePointer()); o.Inherits("QPushButton") && pb.Text() == "Написать" {
				write = pb
			}
		}
		if write == nil {
			t.Error("no «Написать» in user card")
			return
		}
		write.Click()
		waitFor(t, "DM with carol opened", func() bool {
			ch := m.st.Channel(m.current)
			return ch != nil && ch.Type == model.ChannelTypeDirect && m.st.Username(m.st.DMPeer(ch)) == "carol"
		})

		// Обрыв WebSocket: баннер с отсчётом; писать можно — уйдёт, когда
		// связь появится (очередь отправки), подсказка поля говорит об этом.
		m.OnDisconnected(errors.New("test: connection reset"), 9*time.Second)
		waitFor(t, "connection banner", func() bool { return m.banner.f.IsVisible() })
		if !m.composer.frame.IsEnabled() || !strings.Contains(m.composer.edit.PlaceholderText(), "Нет связи") {
			t.Errorf("offline composer: enabled %v, placeholder %q", m.composer.frame.IsEnabled(), m.composer.edit.PlaceholderText())
		}
		shot(t, m, "offline")
		m.OnConnected()
		waitFor(t, "banner hidden", func() bool { return !m.banner.f.IsVisible() })

		SetThemeMode(ThemeDark)
		defer SetThemeMode(ThemeLight)
		town := channelByName(m, "dev", "town-square")
		m.sidebar.SetCurrentItem(m.items[town.Id])
		waitFor(t, "town-square posts", func() bool { return len(m.feed.items) == pageSize })
		shot(t, m, "dark-channel")
	})
}

func descendants(o *qt.QObject) []*qt.QObject {
	var out []*qt.QObject
	for _, c := range o.Children() {
		out = append(out, c)
		out = append(out, descendants(c)...)
	}
	return out
}

func shotWidget(t *testing.T, w *qt.QWidget, name string) {
	dir := os.Getenv("MATTERQUTE_TEST_SHOTS")
	if dir == "" {
		return
	}
	settleFor(300 * time.Millisecond)
	path := filepath.Join(dir, name+".png")
	if !w.Grab().Save(path) {
		t.Errorf("save screenshot %s", path)
	}
}
