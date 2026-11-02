package ui

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/status"
)

// labelled — кнопка (или её строка) с подписью text среди потомков o.
func labelled(o *qt.QObject, text string) *qt.QPushButton {
	for _, c := range descendants(o) {
		if c.Inherits("QPushButton") && qt.UnsafeNewQPushButton(c.UnsafePointer()).Text() == text {
			return qt.UnsafeNewQPushButton(c.UnsafePointer())
		}
		if c.Inherits("QLabel") && qt.UnsafeNewQLabel(c.UnsafePointer()).Text() == text {
			if p := c.Parent(); p != nil && p.Inherits("QPushButton") {
				return qt.UnsafeNewQPushButton(p.UnsafePointer())
			}
		}
	}
	return nil
}

func TestStatus(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		var mood *qt.QPixmap
		var moodText, moodTip string
		m.OnOwnStatus = func(pm *qt.QPixmap, text, tip string) { mood, moodText, moodTip = pm, text, tip }
		if openTown(t, m) == nil {
			return
		}
		ctx := context.Background()
		me := m.s.Me.Id

		// Чужой статус: у Карины «На встрече» — в ленте у имени, в подсказке лички, в карточке.
		carol := m.st.UserByName("carol")
		found := false
		for _, it := range m.feed.items {
			if it.post.UserId == carol.Id && strings.Contains(it.html, `title="На встрече"`) {
				found = true
			}
		}
		if !found {
			t.Error("carol's custom status not next to her name in the feed")
		}
		for _, ch := range m.st.DirectChannels() {
			if ch.Type == model.ChannelTypeDirect && m.st.DMPeer(ch) == carol.Id && m.items[ch.Id].ToolTip(0) != "На встрече" {
				t.Errorf("carol's DM tooltip %q", m.items[ch.Id].ToolTip(0))
			}
		}
		m.showUserCard("carol")
		if card := qt.QApplication_ActivePopupWidget(); card == nil || labelText(card.QObject, "На встрече") == nil {
			t.Error("user card lacks the custom status")
		} else {
			card.Close()
			settle() // закрытие карточки возвращает фокус окну; новое всплывающее открываем после
		}

		// Панель: свой статус не задан, присутствие — «В сети» с галочкой.
		m.showStatusPanel(qt.NewQPoint2(100, 100))
		panel := m.statusPanel
		if labelled(panel.w.QObject, "Установить свой статус…") == nil {
			t.Fatal("no «Установить свой статус…» in the panel")
		}
		shotWidget(t, panel.w, "status-panel")
		panel.w.Close()
		settle()

		// У нижнего края экрана (строка состояния): над точкой и целиком на экране;
		// окно не шире карточки — иначе по краю видно пустое (без композитора — чёрное).
		inScreen := func(what string, w *qt.QWidget) {
			scr, g := w.Screen().AvailableGeometry(), w.Geometry()
			if g.X() < scr.X() || g.Y() < scr.Y() || g.X()+g.Width() > scr.X()+scr.Width() || g.Y()+g.Height() > scr.Y()+scr.Height() {
				t.Errorf("%s %dx%d at %d,%d is off the screen %dx%d", what, g.Width(), g.Height(), g.X(), g.Y(), scr.Width(), scr.Height())
			}
		}
		scr := m.w.Screen().AvailableGeometry()
		bottom := qt.NewQPoint2(scr.X()+10, scr.Y()+scr.Height()-10)
		m.showStatusPanel(bottom)
		panel = m.statusPanel
		inScreen("status panel", panel.w)
		if g := panel.w.Geometry(); g.Y()+g.Height() > bottom.Y() {
			t.Errorf("status panel bottom %d is below the status bar point %d", g.Y()+g.Height(), bottom.Y())
		}
		if pw, fw := panel.w.Width(), panel.frame.Width(); pw != fw {
			t.Errorf("panel window %d px wide, card %d px: empty edge", pw, fw)
		}
		panel.w.Close()
		settle()
		m.openUserCard(carol, qt.NewQPoint2(scr.X()+scr.Width()-5, scr.Y()+scr.Height()-5))
		if card := qt.QApplication_ActivePopupWidget(); card != nil {
			inScreen("user card", card)
			card.Close()
			settle()
		}
		m.showStatusPanel(qt.NewQPoint2(100, 400))
		panel = m.statusPanel
		labelled(panel.w.QObject, "Отошёл").Click()
		waitFor(t, "away", func() bool {
			st, _, err := m.s.Client.GetUserStatus(ctx, me, "")
			return err == nil && st.Status == model.StatusAway && m.st.Status(me) == model.StatusAway
		})

		// «Не беспокоить» на час — конец уходит на сервер в секундах.
		until := status.ExpiresAt("one_hour", time.Now(), time.Time{})
		m.setPresence(model.StatusDnd, until)
		waitFor(t, "dnd for an hour", func() bool {
			st, _, err := m.s.Client.GetUserStatus(ctx, me, "")
			return err == nil && st.Status == model.StatusDnd && st.DNDEndTime == until.Unix() && m.dndEnd == until.Unix()
		})

		// Свой статус из стандартных: «На встрече» на час.
		m.editCustomStatus()
		dlg := m.statusDialog
		labelled(dlg.w.QObject, "На встрече").Click()
		shotWidget(t, dlg.w.QWidget, "status-dialog")
		labelled(dlg.w.QObject, "Сохранить").Click()
		if !waitFor(t, "custom status saved", func() bool {
			cs := status.Current(m.me(), time.Now())
			return cs != nil && cs.Text == "На встрече" && cs.Emoji == "calendar" && cs.Duration == "one_hour" && mood != nil
		}) {
			return
		}
		if !strings.HasPrefix(moodTip, "На встрече · до ") || moodText != "На встрече" {
			t.Errorf("status bar text %q, tip %q", moodText, moodTip)
		}
		u, _, err := m.s.Client.GetMe(ctx, "")
		if cs := u.GetCustomStatus(); err != nil || cs == nil || cs.Text != "На встрече" || cs.ExpiresAt.Before(time.Now().Add(50*time.Minute)) {
			t.Errorf("server custom status %+v, %v", cs, err)
		}

		// Сбросить.
		m.saveCustomStatus(nil)
		waitFor(t, "custom status cleared", func() bool { return status.Current(m.me(), time.Now()) == nil && mood == nil })
	})
}

// labelText — подпись с текстом text среди потомков o.
func labelText(o *qt.QObject, text string) *qt.QLabel {
	for _, c := range descendants(o) {
		if c.Inherits("QLabel") && strings.Contains(qt.UnsafeNewQLabel(c.UnsafePointer()).Text(), text) {
			return qt.UnsafeNewQLabel(c.UnsafePointer())
		}
	}
	return nil
}

// Строка состояния: эмодзи своего статуса и его текст рядом с @user.
func TestShellMood(t *testing.T) {
	onMain(func() {
		s := NewShell()
		defer s.w.Close()
		s.SetUser("alice", "https://mm.example.com")
		s.SetMood(qt.NewQPixmap2(16, 16), "На встрече", "На встрече · до 15:30")
		if s.mood.IsHidden() || s.moodText.IsHidden() || s.moodText.Text() != "На встрече" || s.moodText.ToolTip() != "На встрече · до 15:30" {
			t.Errorf("mood: icon hidden %v, text %q hidden %v, tip %q", s.mood.IsHidden(), s.moodText.Text(), s.moodText.IsHidden(), s.moodText.ToolTip())
		}
		s.w.Resize(900, 400)
		s.w.Show()
		settle()
		shotWidget(t, s.w.StatusBar().QWidget, "status-bar-mood")
		long := strings.Repeat("очень длинный статус ", 20)
		s.SetMood(qt.NewQPixmap2(16, 16), long, long)
		if text := s.moodText.Text(); !strings.HasSuffix(text, "…") || s.moodText.FontMetrics().HorizontalAdvance(text) > moodWidth {
			t.Errorf("long status not elided: %q", text)
		}
		s.SetMood(qt.NewQPixmap2(16, 16), "", "до 15:30") // только эмодзи
		if s.mood.IsHidden() || !s.moodText.IsHidden() {
			t.Error("emoji-only status shows text")
		}
		s.SetMood(nil, "", "")
		if !s.mood.IsHidden() || !s.moodText.IsHidden() {
			t.Error("cleared status still shown")
		}
	})
}
