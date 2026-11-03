package ui

import (
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Автоматический «Отошёл»: простой дольше UserStatusAwayTimeout — серверу
// «не за компьютером», ввод вернулся — «за компьютером»; одно и то же
// дважды не шлём, после переподключения — заново; простой не узнать — молчим.
func TestAutoAway(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	var idleFor time.Duration
	known := true
	was := systemIdle
	systemIdle = func() (time.Duration, bool) { return idleFor, known }
	defer func() { systemIdle = was }()
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		m.Show()
		if !waitFor(t, "sidebar and websocket", func() bool { return m.sidebarOK && m.connectedOnce }) {
			return
		}
		if got := m.st.AwayTimeout(); got != 5*time.Minute {
			t.Errorf("away timeout %v", got)
		}
		me := m.s.Me.Id
		check := func(idle time.Duration, want []bool) bool {
			idleFor = idle
			m.checkAway()
			return waitFor(t, "activity reported", func() bool { return slices.Equal(fake.Activity(), want) })
		}
		if !check(time.Minute, []bool{true}) {
			return
		}
		if !check(10*time.Minute, []bool{true, false}) {
			return
		}
		if !waitFor(t, "away status", func() bool { return m.st.Status(me) == model.StatusAway }) {
			return
		}
		check(11*time.Minute, []bool{true, false}) // уже сообщили
		settleFor(200 * time.Millisecond)
		if !check(0, []bool{true, false, true}) {
			return
		}
		waitFor(t, "online again", func() bool { return m.st.Status(me) == model.StatusOnline })

		// Переподключились — сообщаем заново.
		m.OnConnected()
		if !waitFor(t, "state forgotten on reconnect", func() bool { return m.away == awayUnknown }) {
			return
		}
		check(0, []bool{true, false, true, true})

		// Простой не узнать — ничего не шлём.
		known = false
		m.away = awayUnknown
		idleFor = time.Hour
		m.checkAway()
		settleFor(200 * time.Millisecond)
		if n := len(fake.Activity()); n != 4 {
			t.Errorf("reported without idle time: %v", fake.Activity())
		}
	})
}
