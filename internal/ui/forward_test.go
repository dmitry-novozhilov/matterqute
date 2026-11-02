package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// «Напомнить» — на сервер; напоминание сервера — по-русски со ссылкой;
// «Переслать» — в канал и человеку, с которым ещё нет лички.
func TestRemindForward(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		var p *model.Post
		for _, x := range m.st.Timeline(m.current).Posts {
			if !x.IsSystemMessage() {
				p = x
			}
		}
		at := time.Now().Add(30 * time.Minute).Truncate(time.Minute)
		m.remind(p, at)
		if !waitFor(t, "reminder on server", func() bool { return fake.Reminder(p.Id) == at.Unix() }) {
			return
		}

		// Напоминание от сервера: свой текст по props, ссылка — на сообщение.
		rem := &model.Post{Id: model.NewId(), Type: model.PostTypeReminder, Message: "Hi there, here's your reminder", UserId: fake.UserID("bob")}
		rem.AddProp("team_name", "dev")
		rem.AddProp("post_id", p.Id)
		rem.AddProp("username", "bob")
		if body := m.feed.bodyHTML(rem); !strings.Contains(body, "Напоминание о") || !strings.Contains(body, `href="/dev/pl/`+p.Id+`"`) {
			t.Errorf("reminder body %s", body)
		}
		if got := notificationBody(rem); got != "⏰ Напоминание о сообщении от @bob" {
			t.Errorf("reminder notification %q", got)
		}
		rem.AddProp("team_name", "../evil")
		if body := m.feed.bodyHTML(rem); !strings.Contains(body, "Hi there") {
			t.Errorf("reminder with bad props: %s", body)
		}

		// Переслать в канал с комментарием.
		forward := func(to, comment string) bool {
			d := m.showForward(p)
			var edits []*qt.QLineEdit
			for _, o := range descendants(d.w.QObject) {
				if o.Inherits("QLineEdit") {
					edits = append(edits, qt.UnsafeNewQLineEdit(o.UnsafePointer()))
				}
			}
			if len(edits) != 2 {
				t.Errorf("forward dialog: %d line edits", len(edits))
				return false
			}
			edits[0].SetText(to)
			edits[1].SetText(comment)
			if to == "backend" {
				shotWidget(t, d.w.QWidget, "forward")
			}
			labelled(d.w.QObject, "Переслать").Click()
			return waitFor(t, "forwarded to "+to, func() bool { return d.closed })
		}
		backend := channelByName(m, "dev", "backend").Id
		if !forward("backend", "глянь") {
			return
		}
		if last := fake.Last(backend); last == nil || last.Message != "глянь\n"+m.postLink(p) {
			t.Errorf("forwarded post %+v", last)
		}

		// Человеку без лички — личка создаётся.
		dave := fake.UserID("dave")
		if !forward("dave", "") {
			return
		}
		if !waitFor(t, "DM with dave", func() bool {
			for _, ch := range m.st.DirectChannels() {
				if ch.Type == model.ChannelTypeDirect && m.st.DMPeer(ch) == dave {
					last := fake.Last(ch.Id)
					return last != nil && last.Message == m.postLink(p)
				}
			}
			return false
		}) {
			return
		}
	})
}
