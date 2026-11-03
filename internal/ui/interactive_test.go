package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/mmfake"
)

// Кнопки и список в сообщении интеграции, окно с полями: «Принять» уходит
// на сервер с cookie и правит пост, выбор в списке — со значением, окно —
// проверка обязательного поля здесь, ошибка поля от интеграции, отправка
// значений по типам, отмена — интеграции, раз она просила.
func TestInteractiveMessage(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		m.Show()
		if !waitFor(t, "sidebar and websocket", func() bool { return len(m.items) > 0 && m.connectedOnce }) {
			return
		}
		alerts := channelByName(m, "ops", "alerts")
		m.selectChannel(alerts.Id)
		var alert *model.Post
		html := func() string {
			if i := m.feed.index(alert.Id); i >= 0 {
				return m.feed.items[i].html
			}
			return ""
		}
		if !waitFor(t, "alert with buttons", func() bool {
			for _, it := range m.feed.items {
				if strings.Contains(it.html, actionLink) {
					alert = it.post
					return true
				}
			}
			return false
		}) {
			return
		}
		h := html()
		for _, id := range []string{"ack", "silence", "comment"} {
			if !strings.Contains(h, `href="`+actionLink+alert.Id+":"+id+`"`) {
				t.Errorf("no %s button:\n%s", id, h)
			}
		}
		if strings.Contains(h, actionLink+alert.Id+":stale") || !strings.Contains(h, `src="action:`+alert.Id+`:stale"`) {
			t.Errorf("disabled button should be drawn without a link:\n%s", h)
		}
		shot(t, m, "interactive-message")

		// Кнопка: на сервер с cookie, интеграция правит пост.
		m.feed.onAnchor(actionLink + alert.Id + ":ack")
		if !waitFor(t, "ack edits the post", func() bool { return strings.Contains(html(), "Принял:") }) {
			return
		}
		if calls := fake.Actions(); len(calls) != 1 || calls[0] != (mmfake.ActionCall{PostID: alert.Id, ActionID: "ack", Cookie: "cookie-ack"}) {
			t.Errorf("action calls %+v", calls)
		}
		if strings.Contains(html(), alert.Id+":ack") {
			t.Error("ack button is still there after the integration removed it")
		}

		// Список: варианты меню, выбранное — подписью и на сервер.
		m.feed.onAnchor(actionLink + alert.Id + ":silence")
		var menu *qt.QMenu
		if !waitFor(t, "options menu", func() bool {
			if w := qt.QApplication_ActivePopupWidget(); w != nil && w.Inherits("QMenu") {
				menu = qt.UnsafeNewQMenu(w.UnsafePointer())
			}
			return menu != nil
		}) {
			return
		}
		var titles []string
		var four *qt.QAction
		for _, a := range menu.Actions() {
			titles = append(titles, a.Text())
			if a.Text() == "на 4 часа" {
				four = a
			}
		}
		if four == nil {
			t.Errorf("menu %q", titles)
			return
		}
		four.Trigger()
		menu.Close()
		if !waitFor(t, "silence call", func() bool { return len(fake.Actions()) == 2 }) {
			return
		}
		if c := fake.Actions()[1]; c.ActionID != "silence" || c.Selected != "4h" {
			t.Errorf("select call %+v", c)
		}
		if got := m.feed.chosen[alert.Id+":silence"]; got != "на 4 часа" {
			t.Errorf("select label %q", got)
		}
		waitFor(t, "ephemeral answer", func() bool {
			for _, it := range m.feed.items {
				if it.post.Message == "Заглушено: 4h" {
					return true
				}
			}
			return false
		})

		// Окно с полями.
		m.feed.onAnchor(actionLink + alert.Id + ":comment")
		if !waitFor(t, "integration dialog", func() bool { return m.interactive != nil }) {
			return
		}
		v := m.interactive
		settle()
		shotWidget(t, v.d.w.QWidget, "interactive-dialog")
		if len(v.fields) != 4 {
			t.Errorf("dialog fields %d", len(v.fields))
			return
		}
		var text *qt.QPlainTextEdit
		var number *qt.QLineEdit
		for _, o := range descendants(v.d.w.QObject) {
			switch {
			case o.Inherits("QPlainTextEdit"):
				text = qt.UnsafeNewQPlainTextEdit(o.UnsafePointer())
			case o.Inherits("QLineEdit") && !o.Parent().Inherits("QComboBox"):
				number = qt.UnsafeNewQLineEdit(o.UnsafePointer())
			}
		}
		if text == nil || number == nil {
			t.Error("no comment or number field")
			return
		}
		save := labelled(v.d.w.QObject, "Сохранить")
		save.Click()
		if v.fields[0].err.Text() != "Обязательное поле" || len(fake.Dialogs()) != 0 {
			t.Errorf("empty required field: error %q, sent %d", v.fields[0].err.Text(), len(fake.Dialogs()))
		}
		// Пробелы пропускаем — ошибку поля возвращает интеграция.
		text.SetPlainText("   ")
		save.Click()
		if !waitFor(t, "field error from integration", func() bool { return v.fields[0].err.Text() == "Напишите, что сделали" }) {
			return
		}
		if v.d.closed {
			t.Error("dialog closed on a field error")
			return
		}
		text.SetPlainText("Почистил логи")
		number.SetText("15")
		save.Click()
		if !waitFor(t, "dialog sent", func() bool { return v.d.closed }) {
			return
		}
		ds := fake.Dialogs()
		last := ds[len(ds)-1]
		sub := last.Submission
		if last.CallbackId != "comment" || last.State != alert.Id || last.URL != "https://hooks.example.com/comment" || last.ChannelId != alerts.Id ||
			sub["comment"] != "Почистил логи" || sub["severity"] != "high" || sub["notify"] != false || sub["minutes"] != 15.0 {
			t.Errorf("submission %+v", last)
		}

		// Закрыли без отправки — интеграция узнаёт об отмене.
		m.feed.onAnchor(actionLink + alert.Id + ":comment")
		if !waitFor(t, "dialog again", func() bool { return m.interactive != v }) {
			return
		}
		m.interactive.d.w.Close()
		if !waitFor(t, "cancel sent", func() bool { return len(fake.Dialogs()) == len(ds)+1 }) {
			return
		}
		if c := fake.Dialogs()[len(ds)]; !c.Cancelled || c.Type != dialogCancel || c.CallbackId != "comment" {
			t.Errorf("cancel %+v", c)
		}
	})
}

// Значения полей даты по умолчанию: ISO и относительные.
func TestDialogDate(t *testing.T) {
	now := time.Date(2026, 11, 3, 14, 30, 0, 0, time.Local)
	day := time.Date(2026, 11, 3, 0, 0, 0, 0, time.Local)
	for _, c := range []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"", time.Time{}, false},
		{"today", day, true},
		{"tomorrow", day.AddDate(0, 0, 1), true},
		{"+2d", day.AddDate(0, 0, 2), true},
		{"+3H", now.Add(3 * time.Hour), true},
		{"2026-12-01", time.Date(2026, 12, 1, 0, 0, 0, 0, time.Local), true},
		{"2026-12-01T10:00:00Z", time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC).Local(), true},
		{"когда-нибудь", time.Time{}, false},
	} {
		got, ok := dialogDate(c.in, now)
		if ok != c.ok || !got.Equal(c.want) {
			t.Errorf("dialogDate(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
