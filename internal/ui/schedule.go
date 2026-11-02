package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"slices"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/markdown"
	"matterqute/internal/sched"
	"matterqute/internal/store"
)

// Отложенные сообщения, как в Telegram: «Отправить позже» в поле ввода, список
// «Отложенные» в сайдбаре — правка текста и времени, «Отправить сейчас»,
// удаление. Если сервер умеет сам (Mattermost с лицензией: /posts/schedule),
// хранит и отправляет он — и при закрытом приложении, и они видны в
// веб-клиенте. Иначе хранит и отправляет приложение (файл в каталоге
// настроек), пока оно запущено: опоздавшее при запуске немного — уходит
// сразу, сильно — ждёт решения (sched.Late).

// schedID — псевдо-канал «Отложенные» в сайдбаре (когда они есть).
const schedID = "@scheduled"

// schedTick — как часто приложение проверяет, не пора ли отправить свои отложенные.
const schedTick = 15 * time.Second

type scheduler struct {
	m       *MainWindow
	posts   []*sched.Post
	remote  map[string]*model.ScheduledPost // отложенные на сервере, по id: для правки
	server  bool                            // новые — на сервер: он умеет
	probed  bool                            // спросили сервер, умеет ли
	why     string                          // почему сервер не хранит (его ответ)
	path    string                          // файл отложенных, которые отправляет приложение
	sending map[string]bool                 // уже отправляются
	timer   *qt.QTimer
}

func newScheduler(m *MainWindow, path string) *scheduler {
	s := &scheduler{m: m, path: path, remote: map[string]*model.ScheduledPost{}, sending: map[string]bool{}}
	s.timer = qt.NewQTimer2(m.w.QObject)
	s.timer.OnTimeout(func() { s.tick(time.Now()) })
	return s
}

// start — после загрузки сайдбара (команды известны): свои из файла и
// серверные; после переподключения — серверные заново (события пропущены).
func (s *scheduler) start() {
	if !s.probed {
		s.probed = true
		local, err := sched.Load(s.path)
		if err != nil {
			s.m.log.Warn("scheduled posts", "err", err)
		}
		s.posts = append(s.posts, local...)
		s.timer.Start(int(schedTick / time.Millisecond))
		s.tick(time.Now())
	}
	s.reload()
}

// reload — серверные отложенные; сервер не умеет — новые храним сами.
func (s *scheduler) reload() {
	teams := s.m.st.Teams()
	if len(teams) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(s.m.ctx, requestTimeout)
		defer cancel()
		var all []*model.ScheduledPost
		for i, t := range teams {
			lists, _, err := s.m.s.Client.GetUserScheduledPosts(ctx, t.Id, i == 0)
			if err != nil {
				s.m.ui(func() {
					s.m.log.Info("scheduled posts on the server are unavailable, the app sends them itself", "err", err)
					s.server, s.why = false, commandError(err)
					s.changed()
				})
				return
			}
			for _, l := range lists {
				all = append(all, l...)
			}
		}
		s.m.ui(func() {
			s.server = true
			s.remote = map[string]*model.ScheduledPost{}
			s.posts = slices.DeleteFunc(s.posts, func(p *sched.Post) bool { return p.OnServer })
			for _, sp := range all {
				s.putRemote(sp)
			}
			s.changed()
		})
	}()
}

func fromServer(sp *model.ScheduledPost) *sched.Post {
	p := &sched.Post{ID: sp.Id, ChannelID: sp.ChannelId, RootID: sp.RootId, Message: sp.Message,
		FileIDs: sp.FileIds, At: sp.ScheduledAt, OnServer: true}
	if sp.ErrorCode != "" {
		p.Error = scheduledError(sp.ErrorCode)
	}
	return p
}

// scheduledError — почему сервер не отправил.
func scheduledError(code string) string {
	switch code {
	case model.ScheduledPostErrorCodeChannelArchived:
		return "канал в архиве"
	case model.ScheduledPostErrorCodeChannelNotFound:
		return "канала больше нет"
	case model.ScheduledPostErrorCodeNoChannelPermission, model.ScheduledPostErrorNoChannelMember:
		return "нет доступа к каналу"
	case model.ScheduledPostErrorThreadDeleted:
		return "тред удалён"
	case model.ScheduledPostErrorCodeUserDeleted, model.ScheduledPostErrorCodeUserDoesNotExist, model.ScheduledPostErrorCodeRestrictedDM:
		return "собеседник недоступен"
	}
	return "сервер не смог отправить (" + code + ")"
}

func (s *scheduler) putRemote(sp *model.ScheduledPost) {
	s.remote[sp.Id] = sp
	p := fromServer(sp)
	if i := slices.IndexFunc(s.posts, func(x *sched.Post) bool { return x.ID == p.ID }); i >= 0 {
		s.posts[i] = p
	} else {
		s.posts = append(s.posts, p)
	}
	sched.Sort(s.posts)
}

func (s *scheduler) find(id string) *sched.Post {
	if i := slices.IndexFunc(s.posts, func(p *sched.Post) bool { return p.ID == id }); i >= 0 {
		return s.posts[i]
	}
	return nil
}

// count — сколько отложенных в канале (rootID пусто) или в треде.
func (s *scheduler) count(channelID, rootID string) int {
	n := 0
	for _, p := range s.posts {
		if p.ChannelID == channelID && p.RootID == rootID {
			n++
		}
	}
	return n
}

// create — новое отложенное; не вышло — fail (вернуть текст в поле), вышло — ok.
func (s *scheduler) create(channelID, rootID, text string, fileIDs []string, at time.Time, ok, fail func()) {
	label := sched.Label(at, time.Now())
	if !s.server {
		p := &sched.Post{ID: "local-" + model.NewId(), ChannelID: channelID, RootID: rootID, Message: text, FileIDs: fileIDs, At: at.UnixMilli()}
		s.posts = append(s.posts, p)
		sched.Sort(s.posts)
		s.save()
		s.changed()
		ok()
		s.m.message("Сообщение отправится " + label)
		return
	}
	sp := &model.ScheduledPost{Draft: model.Draft{UserId: s.m.s.Me.Id, ChannelId: channelID, RootId: rootID, Message: text, FileIds: fileIDs},
		ScheduledAt: at.UnixMilli()}
	go func() {
		ctx, cancel := context.WithTimeout(s.m.ctx, requestTimeout)
		defer cancel()
		created, _, err := s.m.s.Client.CreateScheduledPost(ctx, sp)
		s.m.ui(func() {
			if err != nil {
				s.m.fail("schedule post", fmt.Errorf("schedule post in channel %s: %w", channelID, err))
				fail()
				return
			}
			s.putRemote(created) // то же придёт событием; повтор ничего не изменит
			s.changed()
			ok()
			s.m.message("Сообщение отправится " + label)
		})
	}()
}

// update меняет текст или время отложенного id.
func (s *scheduler) update(id string, change func(p *sched.Post)) {
	p := s.find(id)
	if p == nil {
		return
	}
	if !p.OnServer {
		change(p)
		p.Error = ""
		sched.Sort(s.posts)
		s.save()
		s.changed()
		return
	}
	old := s.remote[id]
	if old == nil {
		return
	}
	next := *p
	change(&next)
	sp := &model.ScheduledPost{Draft: model.Draft{CreateAt: old.CreateAt, UserId: old.UserId, ChannelId: old.ChannelId, RootId: old.RootId,
		Message: next.Message, FileIds: old.FileIds, Priority: old.Priority}, Id: id, ScheduledAt: next.At}
	go func() {
		ctx, cancel := context.WithTimeout(s.m.ctx, requestTimeout)
		defer cancel()
		updated, _, err := s.m.s.Client.UpdateScheduledPost(ctx, sp)
		s.m.ui(func() {
			if err != nil {
				s.m.fail("update scheduled post", fmt.Errorf("update scheduled post %s: %w", id, err))
				return
			}
			s.putRemote(updated)
			s.changed()
		})
	}()
}

// remove — удалить отложенное id; done — когда удалено.
func (s *scheduler) remove(id string, done func()) {
	p := s.find(id)
	if p == nil {
		return
	}
	drop := func() {
		s.posts = slices.DeleteFunc(s.posts, func(x *sched.Post) bool { return x.ID == id })
		delete(s.remote, id)
		if !p.OnServer {
			s.save()
		}
		s.changed()
		if done != nil {
			done()
		}
	}
	if !p.OnServer {
		drop()
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(s.m.ctx, requestTimeout)
		defer cancel()
		_, _, err := s.m.s.Client.DeleteScheduledPost(ctx, id)
		s.m.ui(func() {
			if err != nil {
				s.m.fail("delete scheduled post", fmt.Errorf("delete scheduled post %s: %w", id, err))
				return
			}
			drop()
		})
	}()
}

// sendNow — отправить отложенное сразу: обычным сообщением, отложенное — прочь.
func (s *scheduler) sendNow(id string) {
	p := s.find(id)
	if p == nil || s.sending[id] {
		return
	}
	s.sending[id] = true
	post := &model.Post{ChannelId: p.ChannelID, RootId: p.RootID, Message: p.Message, FileIds: p.FileIDs}
	go func() {
		ctx, cancel := context.WithTimeout(s.m.ctx, requestTimeout)
		defer cancel()
		created, _, err := s.m.s.Client.CreatePost(ctx, post)
		s.m.ui(func() {
			delete(s.sending, id)
			if err != nil {
				err = fmt.Errorf("send scheduled post to channel %s: %w", p.ChannelID, err)
				s.m.log.Warn("scheduled post", "err", err)
				if !p.OnServer {
					p.Error = commandError(err)
					s.save()
					s.changed()
				} else {
					s.m.fail("send scheduled post", err)
				}
				return
			}
			s.m.applyPosted(store.NewPost{Post: created})
			s.remove(id, nil)
		})
	}()
}

// tick — свои отложенные, чьё время пришло (опоздание терпимо), — отправить.
func (s *scheduler) tick(now time.Time) {
	for _, p := range slices.Clone(s.posts) {
		if !p.OnServer && p.Due(now) {
			s.sendNow(p.ID)
		}
	}
	if s.m.current == schedID {
		s.m.renderScheduled() // «через минуту» и пропущенные — подписи меняются
	}
}

// applyEvent — событие сервера об отложенном (создано, изменено, удалено —
// в том числе отправлено).
func (s *scheduler) applyEvent(kind model.WebsocketEventType, sp *model.ScheduledPost) {
	if kind == model.WebsocketScheduledPostDeleted {
		if s.find(sp.Id) != nil {
			s.posts = slices.DeleteFunc(s.posts, func(x *sched.Post) bool { return x.ID == sp.Id })
			delete(s.remote, sp.Id)
			s.changed()
		}
		return
	}
	s.putRemote(sp)
	s.changed()
}

func (s *scheduler) save() {
	var local []*sched.Post
	for _, p := range s.posts {
		if !p.OnServer {
			local = append(local, p)
		}
	}
	if err := sched.Save(s.path, local); err != nil {
		s.m.fail("save scheduled posts", err)
	}
}

// changed — список поменялся: сайдбар, подсказки над полями, страница.
func (s *scheduler) changed() { s.m.scheduledChanged() }

// eventScheduledPost — отложенное из события сервера (JSON строкой или объектом).
func eventScheduledPost(data map[string]any) (*model.ScheduledPost, error) {
	raw, ok := data["scheduledPost"]
	if !ok {
		return nil, fmt.Errorf("scheduled post event without scheduledPost")
	}
	var b []byte
	if str, isStr := raw.(string); isStr {
		b = []byte(str)
	} else {
		b, _ = json.Marshal(raw)
	}
	var sp model.ScheduledPost
	if err := json.Unmarshal(b, &sp); err != nil {
		return nil, fmt.Errorf("scheduled post %s: %w", truncate(string(b), 200), err)
	}
	return &sp, nil
}

// scheduledChanged — сайдбар (пункт «Отложенные» есть, только пока они есть),
// подсказки над полями ввода, страница списка.
func (m *MainWindow) scheduledChanged() {
	has := len(m.sched.posts) > 0
	switch {
	case has != (m.items[schedID] != nil):
		if !has && m.current == schedID {
			m.current = ""
		}
		m.fillSidebar()
	default:
		m.sidebar.Viewport().Update()
	}
	m.updateScheduledNotes()
	if m.current == schedID {
		m.renderScheduled()
	}
}

// updateScheduledNotes — «N отложенных» над полем канала и треда.
func (m *MainWindow) updateScheduledNotes() {
	m.composer.SetScheduledNote(m.sched.count(m.current, ""))
	if t := m.thread; t != nil {
		m.panel.composer.SetScheduledNote(m.sched.count(t.channelID, t.rootID))
	} else {
		m.panel.composer.SetScheduledNote(0)
	}
}

// showScheduled — страница «Отложенные».
func (m *MainWindow) showScheduled() {
	m.current = schedID
	m.center.SetCurrentWidget(m.schedPage)
	m.refreshHeader()
	m.renderScheduled()
}

// renderScheduled — список отложенных: где, когда, текст, действия.
func (m *MainWindow) renderScheduled() {
	pal := Pal()
	mode := "Хранит сервер: уйдут и при закрытом приложении, видны в веб-клиенте."
	if !m.sched.server {
		mode = "Сервер отложенные не хранит — их отправляет Matterqute, пока оно запущено."
		if m.sched.why != "" {
			mode += " Сервер ответил: «" + truncate(m.sched.why, 120) + "»."
		}
	}
	m.schedHead.set(iconPixmap("clock", pal.Tx2, 16), "Отложенные", html.EscapeString(mode))
	lay := m.schedRows
	for lay.Count() > 0 {
		if it := lay.TakeAt(0); it != nil && it.Widget() != nil {
			it.Widget().DeleteLater()
		}
	}
	now := time.Now()
	for _, p := range m.sched.posts {
		lay.AddWidget(m.scheduledRow(p, now))
	}
	if len(m.sched.posts) == 0 {
		empty := qt.NewQLabel3("Отложенных сообщений нет")
		empty.SetAlignment(qt.AlignCenter)
		lay.AddWidget(empty.QWidget)
	}
	lay.AddStretch()
}

func (m *MainWindow) scheduledRow(p *sched.Post, now time.Time) *qt.QWidget {
	pal := Pal()
	f := qt.NewQFrame2()
	setName(f.QObject, "schedRow")
	f.SetStyleSheet(fmt.Sprintf(`QFrame#schedRow { border-bottom: 1px solid %s; }
QPushButton { border: none; background: transparent; color: %s; padding: 2px 4px; min-height: 0; }
QPushButton:hover { color: %s; text-decoration: underline; }`, pal.Line, pal.Acc, pal.AccH))
	col := qt.NewQVBoxLayout(f.QWidget)
	col.SetContentsMargins(16, 10, 16, 8)
	col.SetSpacing(4)

	where := "?"
	if ch := m.st.Channel(p.ChannelID); ch != nil {
		where = channelPrefix(ch) + m.st.Title(ch)
	}
	if p.RootID != "" {
		where += " · в треде"
	}
	head := fmt.Sprintf(`<span style="color:%s">%s</span> · <b>%s</b>`, pal.Tx2, html.EscapeString(where), html.EscapeString(sched.Label(p.Time(), now)))
	switch {
	case p.Error != "":
		head += fmt.Sprintf(` · <span style="color:%s">не отправлено: %s</span>`, pal.Dang, html.EscapeString(p.Error))
	case !p.OnServer && p.Missed(now):
		head += fmt.Sprintf(` · <span style="color:%s">не ушло вовремя: приложение было закрыто</span>`, pal.Dang)
	}
	top := qt.NewQLabel3(head)
	top.SetTextFormat(qt.RichText)
	top.SetFont(pixelFont(12, qt.QFont__Normal))
	col.AddWidget(top.QWidget)
	body := qt.NewQLabel3(markdown.PreviewLine(p.Message, markdown.Options{Me: m.s.Me.Username, Palette: pal}))
	body.SetTextFormat(qt.RichText)
	body.SetFont(feedFont())
	col.AddWidget(body.QWidget)

	acts := qt.NewQHBoxLayout2()
	acts.SetSpacing(4)
	act := func(title string, do func()) {
		b := qt.NewQPushButton3(title)
		b.SetCursor(qt.NewQCursor2(qt.PointingHandCursor))
		b.SetFocusPolicy(qt.NoFocus)
		b.OnClicked(do)
		acts.AddWidget(b.QWidget)
	}
	id := p.ID
	act("Изменить", func() { m.editScheduled(id) })
	act("Перенести…", func() {
		if p := m.sched.find(id); p != nil {
			m.showScheduleDialog("Перенести отправку", "Перенести", p.Time(), func(at time.Time) {
				m.sched.update(id, func(p *sched.Post) { p.At = at.UnixMilli() })
			})
		}
	})
	act("Отправить сейчас", func() { m.sched.sendNow(id) })
	act("Удалить", func() { m.sched.remove(id, nil) })
	acts.AddStretch()
	col.AddLayout(acts.QLayout)
	return f.QWidget
}

// editScheduled — «Изменить»: текст отложенного — в поле его канала (треда)
// с оформлением; Enter сохраняет, время не меняется.
func (m *MainWindow) editScheduled(id string) {
	p := m.sched.find(id)
	if p == nil {
		return
	}
	c := m.composer
	m.selectChannel(p.ChannelID)
	if p.RootID != "" {
		m.openThread(p.RootID)
		c = m.panel.composer
	}
	label := "Отложенное (" + sched.Label(p.Time(), time.Now()) + "): " + firstLine(markdown.PlainText(p.Message), 60)
	c.StartEditWith(p.ID, label, markdown.ToBlocks(p.Message))
}

// laterFor — «Отправить позже» в поле c: выбрать время и отложить; где —
// канал и тред этого поля в момент выбора.
func (m *MainWindow) laterFor(c *Composer, where func() (channelID, rootID string)) {
	c.OnLater = func() {
		m.showScheduleDialog("Отправить позже", "Запланировать", time.Now().Add(time.Hour), func(at time.Time) { c.ScheduleAt(at) })
	}
	c.OnSchedule = func(text string, fileIDs []string, at time.Time) {
		ch, root := where()
		if ch == "" {
			c.Restore()
			return
		}
		m.sched.create(ch, root, text, fileIDs, at, c.Sent, c.Restore)
	}
	c.OnShowScheduled = func() { m.selectChannel(schedID) }
}

// showScheduleDialog — когда: варианты (завтра в 9:00…) и своё время.
func (m *MainWindow) showScheduleDialog(title, ok string, initial time.Time, pick func(at time.Time)) {
	d := newDialog(m.w, title)
	m.schedDialog = d
	now := time.Now()
	for _, pr := range sched.Presets(now) {
		b := qt.NewQPushButton3(pr.Title)
		b.SetProperty("role", qt.NewQVariant11("preset"))
		b.SetStyleSheet("text-align: left; padding-left: 10px")
		at := pr.At
		b.OnClicked(func() {
			d.w.Close()
			pick(at)
		})
		d.lay.AddWidget(b.QWidget)
	}
	d.lay.AddSpacing(8)
	d.lay.AddWidget(fieldLabel("Другое время").QWidget)
	edit := newDateTimeEdit(initial)
	d.lay.AddWidget(edit.QWidget)
	hint := qt.NewQLabel2()
	hint.SetProperty("role", qt.NewQVariant11("hint"))
	hint.SetVisible(false)
	d.lay.AddWidget(hint.QWidget)
	d.bottom.AddStretch()
	cancel := qt.NewQPushButton3("Отмена")
	cancel.OnClicked(func() { d.w.Close() })
	okb := qt.NewQPushButton3(ok)
	okb.SetProperty("primary", qt.NewQVariant11("true"))
	okb.SetDefault(true)
	okb.OnClicked(func() {
		at := time.UnixMilli(edit.DateTime().ToMSecsSinceEpoch())
		if !at.After(time.Now().Add(30 * time.Second)) {
			hint.SetText("Это время уже прошло")
			hint.SetVisible(true)
			return
		}
		d.w.Close()
		pick(at)
	})
	d.bottom.AddWidget(cancel.QWidget)
	d.bottom.AddWidget(okb.QWidget)
	d.w.Show()
}
