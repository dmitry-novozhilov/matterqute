package ui

import (
	"time"

	qt "github.com/mappu/miqt/qt6"

	"matterqute/internal/idle"
)

// Автоматический «Отошёл», как у веб-клиента и Mattermost Desktop: система
// простаивает (нет ввода ни в одном приложении, idle.Since) дольше
// UserStatusAwayTimeout сервера (5 минут) — серверу «не за компьютером»,
// ввод вернулся — «за компьютером» (mm.Session.SetActive). Статус, выбранный
// руками («Не беспокоить», «Отошёл»), сервер так не меняет. После
// переподключения состояние сообщаем заново. Простой узнать нечем — молчим.

const (
	awayCheck     = 30 * time.Second // как часто смотреть на простой
	awayCheckAway = 5 * time.Second  // когда «отошёл» — чаще: вернулся — сразу «в сети»
)

// systemIdle — простой системы; в тестах — подделка.
var systemIdle = idle.Since

// awayState — что сообщили серверу: не сообщали, «за компьютером», «отошёл».
type awayState int8

const (
	awayUnknown awayState = iota
	awayActive
	awayIdle
)

// startAwayTimer — смотреть на простой системы, пока окно сервера живо.
func (m *MainWindow) startAwayTimer() {
	m.awayTimer = qt.NewQTimer2(m.w.QObject)
	m.awayTimer.SetSingleShot(true)
	m.awayTimer.OnTimeout(m.checkAway)
	m.awayTimer.Start(int(awayCheck / time.Millisecond))
}

// checkAway — простой в горутине (запрос по DBus может подождать), решение — в главном потоке.
func (m *MainWindow) checkAway() {
	go func() {
		d, ok := systemIdle()
		m.ui(func() {
			next := awayCheck
			defer func() { m.awayTimer.Start(int(next / time.Millisecond)) }()
			if !ok {
				if !m.noIdle {
					m.noIdle = true
					m.log.Info("system idle time unknown: no automatic away status")
				}
				return
			}
			timeout := m.st.AwayTimeout()
			if timeout <= 0 {
				timeout = 5 * time.Minute
			}
			want := awayActive
			if d >= timeout {
				want = awayIdle
				next = awayCheckAway
			}
			if want != m.away && m.s.SetActive(want == awayActive) {
				m.log.Debug("activity status", "active", want == awayActive, "idle", d.Round(time.Second))
				m.away = want
			}
		})
	}()
}
