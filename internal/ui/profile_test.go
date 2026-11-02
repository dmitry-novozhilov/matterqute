package ui

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// Профиль и уведомления: имя, должность, уведомления по умолчанию и свои
// слова-упоминания — на сервер (настройки — целиком), окно знает новое
// («Как в профиле» в меню канала); фото, сменённое где-то ещё, приходит событием.
func TestProfile(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		ctx := context.Background()
		f := m.showProfile()
		shotWidget(t, f.d.w.QWidget, "profile")
		f.first.SetText("Алиса Петровна")
		f.position.SetText("Ведущая разработчица")
		f.mention.SetChecked(true)
		f.email.SetChecked(false)
		f.keys.SetText("деплой, релиз, деплой")
		f.save()
		if !waitFor(t, "profile saved", func() bool { return f.d.closed && m.s.Me.FirstName == "Алиса Петровна" }) {
			return
		}
		u, _, err := s.Client.GetMe(ctx, "")
		if err != nil {
			t.Error(err)
			return
		}
		p := u.NotifyProps
		if u.Position != "Ведущая разработчица" || p[model.DesktopNotifyProp] != model.UserNotifyMention ||
			p[model.EmailNotifyProp] != "false" || p[model.MentionKeysNotifyProp] != "деплой,релиз" {
			t.Errorf("on server: position %q, props %v", u.Position, p)
		}
		town := m.current
		if items := m.notifyItems(m.st.Channel(town)); !strings.Contains(items[0].title, "упоминания") {
			t.Errorf("channel menu does not see the new default: %q", items[0].title)
		}
		if got := mentionTerms(m.me()); got != "@alice деплой релиз" {
			t.Errorf("mention terms %q", got)
		}

		// Ничего не меняли — окно просто закрывается.
		f = m.showProfile()
		f.save()
		settle() // окно удаляется после событий
		if !f.d.closed {
			t.Error("unchanged profile: dialog still open")
		}

		// Фото сменили в другом клиенте — событие user_updated.
		img := image.NewRGBA(image.Rect(0, 0, 8, 8))
		img.Set(1, 1, color.Black)
		var buf bytes.Buffer
		png.Encode(&buf, img)
		if _, err := s.Client.SetProfileImage(ctx, m.s.Me.Id, buf.Bytes()); err != nil {
			t.Error(err)
			return
		}
		if !waitFor(t, "own picture via event", func() bool { return m.me().LastPictureUpdate > 0 && m.s.Me.LastPictureUpdate > 0 }) {
			return
		}
		if m.s.Me.NotifyProps[model.DesktopNotifyProp] != model.UserNotifyMention {
			t.Errorf("event lost notify props: %v", m.s.Me.NotifyProps)
		}
	})
}
