package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/cache"
	"matterqute/internal/config"
	"matterqute/internal/store"
)

// Мгновенный запуск: сайдбар (команды, каналы, членства, категории, треды,
// нужные профили) и хвосты лент — снимком на диске (state.json в кэше
// сервера, 0600). При запуске по сохранённому токену окно строится из
// снимка сразу, токен проверяется следом, а свежее с сервера приходит как
// после обрыва связи: сайдбар заново, ленты сверяются (Stale → Sync/Merge).
// Сервер недоступен — снимок остаётся на экране, связь переподключается
// сама. Пишем при закрытии окна и раз в stateEvery в паузе ввода. В снимке
// переписка — выход из аккаунта и удаление сервера его стирают.

const (
	stateVersion = 1
	statePosts   = pageSize // хвост ленты в снимке
	stateEvery   = 5 * time.Minute
	stateFile    = "state.json"
)

// savedState — снимок окна сервера на диске.
type savedState struct {
	Version   int                        `json:"version"`
	Me        *model.User                `json:"me"`
	Sidebar   *store.Sidebar             `json:"sidebar"`
	Timelines map[string]*store.Timeline `json:"timelines"`
	Outbox    []*model.Post              `json:"outbox,omitempty"` // написанное без связи, ещё не отправленное
}

// statePath — снимок сервера serverURL в общем каталоге кэша cacheDir.
func statePath(cacheDir, serverURL string) string {
	return filepath.Join(cache.DirFor(cacheDir, serverURL), stateFile)
}

// readState — снимок с диска; нет, другой версии или без профиля — nil.
func readState(path string) (*savedState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read saved state: %w", err)
	}
	var st savedState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse saved state %s: %w", path, err)
	}
	if st.Version != stateVersion || st.Me == nil || st.Me.Id == "" || st.Sidebar == nil {
		return nil, nil
	}
	return &st, nil
}

// saveState — снимок на диск; wait — записать сразу (закрытие окна), иначе
// в горутине. Собирается в главном потоке: в нём объекты store. Пока ни
// сервер, ни снимок не дали сайдбара, писать нечего — прежний снимок цел.
func (m *MainWindow) saveState(wait bool) {
	if m.statePath == "" || len(m.st.Teams()) == 0 {
		return
	}
	start := time.Now()
	sb, tls := m.st.Snapshot(statePosts)
	data, err := json.Marshal(&savedState{Version: stateVersion, Me: m.s.Me, Sidebar: sb, Timelines: tls, Outbox: m.outbox.posts()})
	if err != nil {
		m.log.Warn("save state", "err", err)
		return
	}
	path, took := m.statePath, time.Since(start)
	write := func() {
		if err := config.WriteFileAtomic(path, data, 0o600); err != nil {
			m.log.Warn("save state", "err", err)
			return
		}
		m.log.Debug("state saved", "bytes", len(data), "channels", len(tls), "took", took.Round(time.Millisecond))
	}
	if wait {
		write()
		return
	}
	go func() {
		write()
		m.ui(func() {
			if m.statePath == "" { // пока писали, вышли из аккаунта: снимок с перепиской не должен остаться
				os.Remove(path)
			}
		})
	}()
}

// dropState — выход из аккаунта или сервер удалён: снимок прочь и больше не писать.
func (m *MainWindow) dropState() {
	if m.statePath == "" {
		return
	}
	if err := os.Remove(m.statePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		m.log.Warn("drop saved state", "err", err)
	}
	m.statePath = ""
}

// startStateTimer — снимок раз в stateEvery, когда пользователь не вводит.
func (m *MainWindow) startStateTimer() {
	every := int(stateEvery / time.Millisecond)
	t := qt.NewQTimer2(m.w.QObject)
	t.SetSingleShot(true)
	t.OnTimeout(func() {
		if wait := waitInput(lastInput, idleBuild); wait > 0 {
			t.Start(int(wait/time.Millisecond) + 1) // печатают — после паузы
			return
		}
		m.saveState(false)
		t.Start(every)
	})
	t.Start(every)
}

// applyState — окно из снимка: сайдбар и открытый канал сразу, остальные
// ленты — по одной за событие (виджеты лент создаются не разом, окно
// отвечает). Чужой снимок (другой пользователь) не берём.
func (m *MainWindow) applyState(st *savedState) {
	if st.Me.Id != m.s.Me.Id {
		return
	}
	m.st.Restore(st.Sidebar, st.Timelines)
	m.restoreOutbox(st.Outbox) // в ленты — когда построятся, на сервер — когда будет связь
	m.restored = true
	m.composer.SetPriorities(m.st.PostPriority())
	m.fillSidebar()
	m.ownStatusChanged()
	for id := range st.Timelines {
		mainthread.Start(func() {
			tl := m.st.Timeline(id)
			if m.closed || tl == nil || !tl.Loaded || m.st.Channel(id) == nil {
				return
			}
			if f := m.feeds[id]; f == nil || !f.ready {
				m.showTimeline(id, tl)
			}
		})
	}
}

// setMe — свой профиль с сервера (окно из снимка, правка профиля).
func (m *MainWindow) setMe(u *model.User) { m.applyUser(u) }
