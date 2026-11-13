package ui

import (
	"slices"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// Ленты каналов готовы заранее: у каждого канала и лички — своя Feed (свой
// QTextBrowser со своим документом) в стеке, на экране — одна. Переключение
// канала — смена виджета в стеке (миллисекунды), без разбора HTML и раскладки:
// на длинной ленте это секунды. События WebSocket правят ленту своего канала,
// даже скрытую. Скрытые рисуются фоном, по порции за такт, когда пользователь
// не печатает и не крутит (buildStep), и держат размер видимой (fitTo): иначе
// Qt раскладывал бы документ заново при показе.

func (m *MainWindow) initFeeds(deps FeedDeps) {
	watchInput()
	m.feedDeps = deps
	m.feeds = map[string]*Feed{}
	m.feedStack = qt.NewQStackedWidget2()
	m.buildTimer = qt.NewQTimer2(m.w.QObject)
	m.buildTimer.SetSingleShot(true)
	m.buildTimer.OnTimeout(m.buildStep)
	m.fitTimer = qt.NewQTimer2(m.w.QObject)
	m.fitTimer.SetSingleShot(true)
	m.fitTimer.SetInterval(300) // размер устоялся (окно тянут мышью)
	m.fitTimer.OnTimeout(func() {
		m.eachFeed(func(f *Feed) { f.needFit = f != m.feed })
		m.buildSoon()
	})
	m.feedStack.OnResizeEvent(func(super func(*qt.QResizeEvent), e *qt.QResizeEvent) {
		super(e)
		m.fitTimer.Start2()
	})
	m.blank = NewFeed(deps)
	m.feedStack.AddWidget(m.blank.Widget())
	m.feed = m.blank
	rebuild := qt.NewQTimer2(m.w.QObject)
	rebuild.OnTimeout(m.rebuildFeeds)
	rebuild.Start(int(feedRebuildCheck / time.Millisecond))
}

// Пересборка документов скрытых лент (Feed.Rebuild): документ собран час
// назад или раньше и держит хотя бы feedRebuildMin фреймов, оставшихся от
// перерисованных постов (~2 КБ каждый; меньше — разбирать HTML всей ленты
// заново дороже, чем держать). Проверка — раз в feedRebuildCheck.
const (
	feedRebuildEvery = time.Hour
	feedRebuildCheck = 10 * time.Minute
	feedRebuildMin   = 100
)

// rebuildFeeds — скрытые ленты, которым пора, — заново, фоном (buildStep).
func (m *MainWindow) rebuildFeeds() {
	if m.closed {
		return
	}
	for _, id := range m.feedOrder {
		f := m.feeds[id]
		if f == m.feed || time.Since(f.built) < feedRebuildEvery {
			continue
		}
		if n := f.orphans(); n >= feedRebuildMin && f.Rebuild() {
			m.log.Debug("feed rebuilt", "channel", id, "orphan_frames", n, "posts", len(f.pending))
		}
	}
}

// feedFor — лента канала id; нет — новая, скрытая.
func (m *MainWindow) feedFor(id string) *Feed {
	if f := m.feeds[id]; f != nil {
		return f
	}
	f := NewFeed(m.feedDeps)
	f.shown = false
	f.OnNeedOlder = func() {
		if m.current == id {
			m.loadOlder()
		}
	}
	f.OnOpenThread = m.openThread
	m.wireFeed(f)
	f.OnRetry = func() {
		f.Loading()
		m.loadTimeline(id)
	}
	f.OnWork = m.buildSoon
	// Стиль (QSS) — сейчас, на пустом документе: при первом показе он меняет
	// шрифт виджета, и документ раскладывался бы заново.
	f.view.EnsurePolished()
	m.feedStack.AddWidget(f.Widget())
	f.fitTo(m.feedStack.Size())
	m.feeds[id] = f
	m.feedOrder = append(m.feedOrder, id)
	return f
}

// showFeed — на экран ленту f, прежнюю — убрать (она помнит своё место).
func (m *MainWindow) showFeed(f *Feed) {
	if f == m.feed {
		return
	}
	m.feed.SetShown(false)
	m.feed = f
	f.needFit = false
	f.fitTo(m.feedStack.Size())
	m.feedStack.SetCurrentWidget(f.Widget())
	f.SetShown(true)
}

// dropFeed — канала больше нет (вышли из него): его ленту прочь.
func (m *MainWindow) dropFeed(id string) {
	f := m.feeds[id]
	if f == nil {
		return
	}
	if f == m.feed {
		m.showFeed(m.blank)
	}
	delete(m.feeds, id)
	m.feedOrder = slices.DeleteFunc(m.feedOrder, func(x string) bool { return x == id })
	f.OnWork = nil
	f.dispose()
	m.feedStack.RemoveWidget(f.Widget())
	f.Widget().DeleteLater()
}

// resetFeeds — ленты в другом режиме (свёрнутые треды включили или
// выключили): все — заново, когда загрузятся.
func (m *MainWindow) resetFeeds() {
	for _, f := range m.feeds {
		f.Reset("")
	}
}

// eachFeed — ленты всех каналов и лента без канала.
func (m *MainWindow) eachFeed(do func(f *Feed)) {
	for _, f := range m.feeds {
		do(f)
	}
	do(m.blank)
}

// feedWith — лента канала, если в ней пост id (отрисованный или в очереди).
func (m *MainWindow) feedWith(channelID, id string) *Feed {
	if f := m.feeds[channelID]; f != nil && f.Has(id) {
		return f
	}
	return nil
}

func (m *MainWindow) buildSoon() {
	if !m.closed && !m.buildTimer.IsActive() {
		m.buildTimer.Start(0)
	}
}

// buildStep — порция фоновой работы одной скрытой ленты, когда ввод утих
// (input.go): сначала размер (сменился у ленты на экране) — с раскладкой
// документа сразу, чтобы Qt не доделывал её своим таймером посреди набора;
// потом отрисовка (в порядке, в каком ленты появлялись: непрочитанное,
// лички, остальное). Следующая порция — после событий окна.
func (m *MainWindow) buildStep() {
	if wait := waitInput(lastInput, idleBuild); wait > 0 {
		m.buildTimer.Start(int(wait.Milliseconds()) + 1)
		return
	}
	var next *Feed
	m.eachFeed(func(f *Feed) {
		if next == nil && f != m.feed && f.needFit {
			next = f
		}
	})
	if f := next; f != nil {
		f.needFit = false
		f.fitTo(m.feedStack.Size())
		f.doc.Size()
		m.buildSoon()
		return
	}
	for _, id := range m.feedOrder {
		if f := m.feeds[id]; f != m.feed && f.hasWork() {
			f.drain()
			m.buildSoon()
			return
		}
	}
}

// mergeFeed — лента канала сверена с сервером после обрыва (Timeline.Sync).
func (m *MainWindow) mergeFeed(channelID string, changed, removed []*model.Post) {
	if f := m.feeds[channelID]; f != nil && f.ready {
		f.Merge(changed, removed)
	}
}
