package ui

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/cache"
	"matterqute/internal/emoji"
	"matterqute/internal/markdown"
	"matterqute/internal/mm"
	"matterqute/internal/store"
)

// Сколько постов грузить за раз.
const pageSize = 60

// MainWindow — окно одного сервера: страница внутри Shell (или отдельное окно
// в тестах). Все методы — из главного потока Qt; горутины возвращаются в него
// через ui(), который проверяет closed.
type MainWindow struct {
	w      *qt.QWidget
	s      *mm.Session
	st     *store.Store
	log    *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
	closed bool

	split          *qt.QSplitter
	columns        columns    // ширины сайдбара и панели треда
	saveColumns    *qt.QTimer // сохранить ширины, когда разделитель отпустили
	sidebar        *qt.QTreeWidget
	items          map[string]*qt.QTreeWidgetItem // по id канала; threadsID — «Треды»
	center         *qt.QStackedWidget             // channelPage или threadsPage
	channelPage    *qt.QWidget
	head           *header
	banner         *banner
	feed           *Feed              // лента на экране: канала или blank (feeds.go)
	feeds          map[string]*Feed   // ленты каналов, готовые заранее
	feedOrder      []string           // в порядке появления: фоном рисуются по нему
	blank          *Feed              // пока канал не выбран
	feedStack      *qt.QStackedWidget // все ленты, на экране — feed
	feedDeps       FeedDeps
	buildTimer     *qt.QTimer // порция фоновой отрисовки скрытых лент
	fitTimer       *qt.QTimer // размер скрытых — когда размер ленты на экране устоится
	composer       *Composer
	threadsPage    *qt.QWidget
	schedPage      *qt.QWidget // «Отложенные»
	schedHead      *header
	schedRows      *qt.QVBoxLayout
	schedDialog    *dialog // «Отправить позже» / «Перенести»
	sched          *scheduler
	drafts         *drafts         // черновики на сервере или в файле (drafts.go)
	search         *searchPanel    // поиск по сообщениям — в правой колонке
	typing         *typingState    // кто где печатает
	searchBtn      *qt.QToolButton // лупа в шапке канала
	pinsBtn        *qt.QToolButton // закреплённые в канале — в шапке
	savedBtn       *qt.QToolButton // сохранённые — в шапке
	manualUnread   map[string]bool // каналы и треды, помеченные непрочитанными: сами не отмечаем прочитанными (postactions.go)
	threadsHead    *header
	threadsList    *qt.QListWidget
	panel          *ThreadPanel
	thread         *threadView // открытый в панели тред; nil — панель закрыта
	assets         *Assets
	notifier       Notifier           // nil — уведомлений нет (нет шины или сервера уведомлений)
	notes          map[string]uint32  // показанное уведомление по каналу или треду: следующее его заменит
	players        map[string]*player // открытые плееры по id файла
	deleteDialog   *dialog            // «Удалить сообщение?»
	channelDialog  *dialog            // обзор, создание канала, «покинуть»
	switcherDialog *dialog            // последнее окно быстрого перехода (Ctrl+K)
	viewer         *imageViewer       // картинка во весь экран; nil — закрыта

	preloadQueue []string // каналы, чьи ленты ещё грузить заранее
	preloading   int      // сколько лент грузится заранее прямо сейчас

	suggestTimer   *qt.QTimer // запрос подсказок к серверу — после паузы в наборе
	suggestPending func()

	scrollTo     string       // после загрузки ленты прокрутить к этому посту (ссылка на сообщение)
	pendingPath  string       // путь сервера из ссылки снаружи: открыть после загрузки сайдбара
	dndEnd       int64        // свой конец «не беспокоить», секунды; 0 — неизвестен или без срока
	statusPanel  *card        // последняя открытая панель статуса (для тестов)
	statusDialog *dialog      // последнее открытое окно своего статуса
	picker       *emojiPicker // последнее открытое окно выбора эмодзи
	recent       emoji.Recent // недавние эмодзи (настройка recent_emojis)
	customEmoji  []string     // имена кастомных эмодзи сервера
	emojiLoaded  bool
	counts       Counts // последнее отданное в OnCounts; -1 — ещё не отдавали
	retryNow     chan struct{}
	unsubscribe  func()

	current         string // открытый канал или threadsID
	initialChannel  string // открыть после первой загрузки сайдбара
	selectAfterLoad string // открыть после перезагрузки сайдбара (новая личка)
	sidebarLoading  bool
	sidebarDirty    bool // пока грузили, пришло событие, требующее перезагрузки
	connectedOnce   bool
	online          bool

	OnChannelOpened   func(id string)
	OnConnection      func(ConnState)
	OnMessage         func(text string)                // короткое сообщение в строке состояния окна
	OnColumnsChanged  func(sidebar, thread int)        // пользователь передвинул разделитель
	OnRaise           func()                           // показать окно поверх остальных (клик по уведомлению)
	OnCounts          func(Counts)                     // изменилось, сколько ждёт сообщений (трей)
	OnOtherServerLink func(link string) bool           // ссылка на другой добавленный сервер: открыть там; false — не он
	OnOwnStatus       func(pm *qt.QPixmap, tip string) // свой статус: эмодзи и подсказка, nil — нет
}

// Ширины колонок по дизайну: сайдбар 240 (от 180 до 360), тред 380 (от 280).
const (
	sidebarWidth, sidebarMin, sidebarMax = 240, 180, 360
	threadWidth, threadMin               = 380, 280
)

type columns struct{ sidebar, thread int }

// cacheDir — общий каталог кэша; у каждого сервера в нём свой подкаталог.
func NewMainWindow(s *mm.Session, log *slog.Logger, initialChannel, cacheDir string) *MainWindow {
	ctx, cancel := context.WithCancel(context.Background())
	m := &MainWindow{
		w:       qt.NewQWidget2(),
		s:       s,
		st:      store.New(s.Me),
		log:     log.With("server", s.ServerURL),
		ctx:     ctx,
		cancel:  cancel,
		items:   map[string]*qt.QTreeWidgetItem{},
		notes:   map[string]uint32{},
		players: map[string]*player{},
		counts:  Counts{-1, -1},

		manualUnread: map[string]bool{},
		retryNow:     make(chan struct{}, 1),

		initialChannel: initialChannel,
	}
	m.assets = NewAssets(cache.New(cacheDir, s.ServerURL, s.Client), ctx, m.ui, m.log)
	m.w.Resize(1280, 774)
	m.w.OnEvent(func(super func(*qt.QEvent) bool, ev *qt.QEvent) bool {
		if ev.Type() == qt.QEvent__WindowActivate {
			// Пока окно было неактивно, в открытом могло накопиться непрочитанное.
			if unread, _ := m.st.Unread(m.current); unread && !m.manualUnread[m.current] {
				m.markViewed(m.current, "")
			}
			if t := m.thread; t != nil && t.channelID != "" && !m.manualUnread[t.rootID] {
				if th := m.st.Thread(t.rootID); th != nil && th.UnreadReplies > 0 {
					m.markThreadRead(t.rootID, t.channelID)
				}
			}
		}
		return super(ev)
	})

	m.setupSidebar()

	m.head = newHeader("channelHeader", false, m.handleLink)
	m.head.set(nil, "Выберите канал", "")
	m.banner = newBanner(hostOf(s.ServerURL))
	m.banner.OnRetry = func() {
		select {
		case m.retryNow <- struct{}{}:
		default:
		}
	}

	deps := FeedDeps{Names: m.st.Username, Users: m.st.User, Me: s.Me, Assets: m.assets, ChannelExists: m.channelExists, Log: m.log,
		LinkPreviews: m.st.LinkPreviews, QuickReactions: m.quickReactions, EmojiImage: m.emojiImage, Saved: m.st.Saved}
	m.initFeeds(deps)

	// Свои статусы со сроком истекают сами: раз в минуту перерисовываем, где они видны.
	statusTimer := qt.NewQTimer2(m.w.QObject)
	statusTimer.OnTimeout(func() {
		m.ownStatusChanged()
		m.refreshItem("")
	})
	statusTimer.Start(60_000)
	m.suggestTimer = qt.NewQTimer2(m.w.QObject)
	m.suggestTimer.SetSingleShot(true)
	m.suggestTimer.OnTimeout(func() {
		if f := m.suggestPending; f != nil {
			m.suggestPending = nil
			f()
		}
	})
	m.composer = NewComposer()
	m.composer.Suggest = m.suggester(func() string { return m.current })
	m.composer.OnSend = m.send
	m.composer.OnEdit = func(id, text string, blocks []markdown.Block) { m.saveEdit(m.feed)(id, text, blocks) }
	m.composer.OnEditLast = func() { m.editLast(m.feed) }
	m.composer.OnCommand = func(text string) { m.runCommand(m.composer, m.current, "", text) }
	m.sched = newScheduler(m, filepath.Join(cacheDir, "scheduled.json"))
	m.initTyping()
	m.composer.OnTyping = func() { m.userTyping(m.current, "") }
	m.laterFor(m.composer, func() (string, string) {
		if m.st.Channel(m.current) == nil {
			return "", ""
		}
		return m.current, ""
	})
	m.composer.OnUpload = func(id int, name, path string, data []byte, progress func(int)) {
		m.upload(m.composer, m.current, id, name, path, data, progress)
	}
	m.composer.SetEnabled(false)

	m.channelPage = qt.NewQWidget2()
	cl := qt.NewQVBoxLayout(m.channelPage)
	cl.SetContentsMargins(0, 0, 0, 0)
	cl.SetSpacing(0)
	cl.AddWidget(m.head.f.QWidget)
	cl.AddWidget(m.banner.f.QWidget)
	cl.AddWidget2(m.feedStack.QWidget, 1)
	cl.AddWidget(m.composer.Widget())

	m.threadsPage = qt.NewQWidget2()
	tl := qt.NewQVBoxLayout(m.threadsPage)
	tl.SetContentsMargins(0, 0, 0, 0)
	tl.SetSpacing(0)
	m.threadsHead = newHeader("channelHeader", false, nil)
	m.threadsList = qt.NewQListWidget2()
	m.setupThreadsList()
	tl.AddWidget(m.threadsHead.f.QWidget)
	tl.AddWidget2(m.threadsList.QWidget, 1)

	m.schedPage = qt.NewQWidget2()
	sl := qt.NewQVBoxLayout(m.schedPage)
	sl.SetContentsMargins(0, 0, 0, 0)
	sl.SetSpacing(0)
	m.schedHead = newHeader("channelHeader", false, nil)
	sl.AddWidget(m.schedHead.f.QWidget)
	schedScroll := qt.NewQScrollArea2()
	schedScroll.SetFrameShape(qt.QFrame__NoFrame)
	schedScroll.SetWidgetResizable(true)
	schedBody := qt.NewQWidget2()
	m.schedRows = qt.NewQVBoxLayout(schedBody)
	m.schedRows.SetContentsMargins(0, 0, 0, 0)
	m.schedRows.SetSpacing(0)
	schedScroll.SetWidget(schedBody)
	sl.AddWidget2(schedScroll.QWidget, 1)

	m.center = qt.NewQStackedWidget2()
	m.center.AddWidget(m.channelPage)
	m.center.AddWidget(m.threadsPage)
	m.center.AddWidget(m.schedPage)
	m.center.SetMinimumWidth(360)

	deps.Thread = true
	m.panel = NewThreadPanel(deps, m.closeThread)
	m.wireFeed(m.panel.feed)
	sdeps := deps
	sdeps.Where = m.postWhere
	m.search = newSearchPanel(sdeps, m.closeSearch, m.runSearch)
	m.drafts = newDrafts(m, filepath.Join(cacheDir, "drafts.json"))
	m.composer.OnDraft = func(key string) { m.drafts.changed(m.composer, key) }
	m.panel.composer.OnDraft = func(key string) { m.drafts.changed(m.panel.composer, key) }
	m.wireFeed(m.search.feed)
	m.search.feed.OnOpenThread = m.openThread
	m.panel.back = m.panel.head.addTool("Назад к результатам", func() {
		m.closeThread()
		m.showRight(m.search.w)
	})
	m.panel.back.SetVisible(false)
	m.pinsBtn = m.head.addTool("Закреплённые в канале", func() { m.toggleList(listPinned) })
	m.savedBtn = m.head.addTool("Сохранённые сообщения", func() { m.toggleList(listSaved) })
	m.searchBtn = m.head.addTool("Поиск по сообщениям (Ctrl+F)", m.openSearch)
	// Ctrl+K — быстрый переход, как в веб-клиенте (ссылка в поле ввода — Ctrl+Alt+K).
	sw := qt.NewQAction5("Перейти к каналу", m.w.QObject)
	sw.SetShortcut(qt.NewQKeySequence2("Ctrl+K"))
	sw.SetShortcutContext(qt.WidgetWithChildrenShortcut)
	sw.OnTriggered(m.showSwitcher)
	m.w.AddAction(sw)
	find := qt.NewQAction5("Поиск", m.w.QObject)
	find.SetShortcut(qt.NewQKeySequence2("Ctrl+F"))
	find.SetShortcutContext(qt.WidgetWithChildrenShortcut)
	find.OnTriggered(m.openSearch)
	m.w.AddAction(find)
	m.panel.composer.OnSend = m.sendReply
	m.panel.composer.OnEdit = m.saveEdit(m.panel.feed)
	m.panel.composer.OnEditLast = func() { m.editLast(m.panel.feed) }
	m.laterFor(m.panel.composer, func() (string, string) {
		if t := m.thread; t != nil && t.channelID != "" {
			return t.channelID, t.rootID
		}
		return "", ""
	})
	m.panel.composer.OnTyping = func() {
		if t := m.thread; t != nil {
			m.userTyping(t.channelID, t.rootID)
		}
	}
	m.panel.composer.OnCommand = func(text string) {
		if t := m.thread; t != nil {
			m.runCommand(m.panel.composer, t.channelID, t.rootID, text)
		}
	}
	m.panel.composer.Suggest = m.suggester(func() string {
		if t := m.thread; t != nil {
			return t.channelID
		}
		return ""
	})
	m.panel.composer.OnUpload = func(id int, name, path string, data []byte, progress func(int)) {
		if t := m.thread; t != nil {
			m.upload(m.panel.composer, t.channelID, id, name, path, data, progress)
		}
	}

	m.split = qt.NewQSplitter2()
	m.split.SetHandleWidth(1)
	m.split.SetChildrenCollapsible(false)
	m.split.AddWidget(m.sidebar.QWidget)
	m.split.AddWidget(m.center.QWidget)
	m.split.AddWidget(m.panel.w)
	m.split.AddWidget(m.search.w)
	m.split.SetStretchFactor(1, 1)
	m.sidebar.SetMinimumWidth(sidebarMin)
	m.sidebar.SetMaximumWidth(sidebarMax)
	m.panel.w.SetMinimumWidth(threadMin)
	m.search.w.SetMinimumWidth(threadMin)
	m.SetColumns(0, 0)
	// splitterMoved идёт на каждый пиксель перетаскивания: сохраняем, когда
	// разделитель отпустили.
	m.saveColumns = qt.NewQTimer2(m.w.QObject)
	m.saveColumns.SetSingleShot(true)
	m.saveColumns.SetInterval(500)
	m.saveColumns.OnTimeout(func() {
		if m.OnColumnsChanged != nil {
			m.OnColumnsChanged(m.columns.sidebar, m.columns.thread)
		}
	})
	m.split.OnSplitterMoved(func(int, int) {
		sizes := m.split.Sizes()
		m.columns.sidebar = sizes[0]
		switch {
		case m.panel.w.IsVisible():
			m.columns.thread = sizes[2]
		case m.search.w.IsVisible():
			m.columns.thread = sizes[3]
		}
		m.saveColumns.Start2()
	})
	root := qt.NewQVBoxLayout(m.w)
	root.SetContentsMargins(0, 0, 0, 0)
	root.AddWidget(m.split.QWidget)

	m.unsubscribe = OnThemeChanged(m.restyle)
	m.restyle()
	return m
}

func (m *MainWindow) Widget() *qt.QWidget { return m.w }

// SetColumns — ширины сайдбара и панели треда с прошлого раза; 0 — по
// умолчанию. Центр забирает остаток: у него одного ненулевой stretch.
func (m *MainWindow) SetColumns(sidebar, thread int) {
	if sidebar <= 0 {
		sidebar = sidebarWidth
	}
	if thread <= 0 {
		thread = threadWidth
	}
	m.columns = columns{min(max(sidebar, sidebarMin), sidebarMax), max(thread, threadMin)}
	m.split.SetSizes([]int{m.columns.sidebar, 1280 - m.columns.sidebar - m.columns.thread, m.columns.thread})
}

// Columns — текущие ширины сайдбара и панели треда (последняя, с которой
// панель была открыта).
func (m *MainWindow) Columns() (sidebar, thread int) { return m.columns.sidebar, m.columns.thread }

// Start грузит сайдбар и подключает realtime.
func (m *MainWindow) Start() {
	m.reloadSidebar()
	go m.s.RunRealtime(m.ctx, m, m.retryNow)
}

// Show — отдельным окном (тесты).
func (m *MainWindow) Show() {
	m.w.Show()
	m.Start()
}

// Close останавливает работу окна сервера без выхода из аккаунта; виджеты
// удаляет владелец (Shell.RemovePage) или Qt при закрытии окна.
func (m *MainWindow) Close() {
	if m.closed {
		return
	}
	m.drafts.save(true) // набранное за последние доли секунды — тоже
	m.closed = true
	m.cancel()
	// Все подписки на тему — до удаления виджетов: смена темы после выхода
	// из аккаунта иначе полезет в удалённые объекты.
	m.unsubscribe()
	m.panel.dispose()
	m.search.dispose()
	m.eachFeed((*Feed).dispose)
	m.composer.dispose()
	for _, pl := range m.players {
		pl.w.Close()
	}
	if m.viewer != nil {
		m.viewer.w.Close()
	}
	m.assets.stopAnimations()
	m.assets.wait(2 * time.Second)
	m.w.Close()
}

// restyle — смена темы: иконки и цвета, нарисованные кодом.
func (m *MainWindow) restyle() {
	m.banner.restyle()
	if !style.typingShow {
		m.typing.who = map[typingKey]map[string]time.Time{}
	}
	m.updateTypingNotes()
	m.searchBtn.SetIcon(icon("search", Pal().Tx2, 18))
	m.pinsBtn.SetIcon(icon("pin", Pal().Tx2, 18))
	m.savedBtn.SetIcon(icon("bookmark", Pal().Tx2, 18))
	m.panel.back.SetIcon(icon("arrow-left", Pal().Tx2, 18))
	m.refreshHeader()
	m.sidebar.DoItemsLayout() // шрифт сайдбара мог поменяться — с ним высота строк
	m.sidebar.Viewport().Update()
	m.threadsList.Viewport().Update()
}

func (m *MainWindow) setConnection(c ConnState) {
	if m.OnConnection != nil {
		m.OnConnection(c)
	}
}

// slowUI — дольше этого задача из горутины держит главный поток: окно
// не отвечает. С MATTERQUTE_DEBUG=1 такие пишутся в лог с местом вызова ui.
const slowUI = 100 * time.Millisecond

// ui запускает f в главном потоке, если окно ещё живо.
func (m *MainWindow) ui(f func()) {
	var from string
	if m.log.Enabled(context.Background(), slog.LevelDebug) {
		if _, file, line, ok := runtime.Caller(1); ok {
			from = fmt.Sprintf("%s:%d", filepath.Base(file), line)
		}
	}
	mainthread.Start(func() {
		if m.closed {
			return
		}
		start := time.Now()
		f()
		if d := time.Since(start); from != "" && d > slowUI {
			m.log.Debug("main thread busy", "took", d.Round(time.Millisecond), "from", from)
		}
	})
}

// fail показывает ошибку фоновой операции в строке состояния и в логе.
func (m *MainWindow) fail(op string, err error) {
	m.log.Error(op, "err", err)
	m.message(err.Error())
}

func (m *MainWindow) message(text string) {
	if m.OnMessage != nil {
		m.OnMessage(text)
	}
}

// withUsers догружает недостающие профили и потом вызывает then. then
// выполняется позже — состояние (открытый канал и т. п.) там надо перепроверять.
func (m *MainWindow) withUsers(ids []string, then func()) {
	missing := m.st.MissingUsers(ids)
	if len(missing) == 0 {
		then()
		return
	}
	go func() {
		users, _, err := m.s.Client.GetUsersByIds(m.ctx, missing)
		m.ui(func() {
			if err != nil {
				m.log.Warn("load users", "ids", missing, "err", err)
			} else {
				m.st.AddUsers(users)
			}
			then()
		})
	}()
}

// Сайдбар

func (m *MainWindow) reloadSidebar() {
	if m.sidebarLoading {
		m.sidebarDirty = true
		return
	}
	m.sidebarLoading = true
	go func() {
		sb, err := m.fetchSidebar()
		m.ui(func() {
			m.sidebarLoading = false
			if err != nil {
				m.fail("load sidebar", err)
			} else {
				crtChanged := sb.CRT != m.st.CRT()
				previewsChanged := sb.LinkPreviews != m.st.LinkPreviews()
				m.st.SetSidebar(sb)
				for _, st := range sb.Statuses {
					if st.UserId == m.s.Me.Id {
						m.dndEnd = st.DNDEndTime
					}
				}
				m.ownStatusChanged()
				for id := range m.feeds {
					if m.st.Channel(id) == nil {
						m.dropFeed(id) // канала больше нет
					}
				}
				if previewsChanged && !crtChanged {
					m.eachFeed((*Feed).invalidateAll)
					m.panel.feed.renderAll()
				}
				if crtChanged {
					// Ленты загружены в другом режиме (с ответами или без).
					m.st.DropTimelines()
					m.resetFeeds()
					m.thread = nil
					m.panel.w.SetVisible(false)
					if ch := m.st.Channel(m.current); ch != nil {
						// fillSidebar выделит его заново, и он откроется в новом режиме.
						m.current, m.initialChannel = "", ch.Id
					}
				}
				m.fillSidebar() // открытый канал начинает грузиться первым
				m.preload()
				m.drafts.sidebarLoaded(sb.SyncDrafts)
				if !m.sched.probed {
					m.sched.start() // команды известны: отложенные с сервера и свои
				}
			}
			if m.sidebarDirty {
				m.sidebarDirty = false
				m.reloadSidebar()
			}
		})
	}()
}

func (m *MainWindow) fetchSidebar() (*store.Sidebar, error) {
	ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
	defer cancel()
	c, me := m.s.Client, m.s.Me

	teams, _, err := c.GetTeamsForUser(ctx, me.Id, "")
	if err != nil {
		return nil, fmt.Errorf("load teams of %s: %w", me.Username, err)
	}
	sb := &store.Sidebar{Teams: teams}
	// При ошибке часть настроек может быть прочитана, остальное — по умолчанию (без CRT).
	settings, err := m.s.FetchSettings(ctx)
	if err != nil {
		m.log.Warn("load server settings", "err", err)
		settings.LinkPreviews = true
	}
	sb.CRT, sb.MaxFileSize, sb.LinkPreviews, sb.SyncDrafts = settings.CRT, settings.MaxFileSize, settings.LinkPreviews, settings.SyncDrafts
	peers := map[string]bool{}
	for _, t := range teams {
		chs, _, err := c.GetChannelsForTeamForUser(ctx, t.Id, me.Id, false, "")
		if err != nil {
			return nil, fmt.Errorf("load channels of team %s (%s): %w", t.Name, t.Id, err)
		}
		sb.Channels = append(sb.Channels, chs...)
		for _, ch := range chs {
			if ch.Type == model.ChannelTypeDirect {
				if p := ch.GetOtherUserIdForDM(me.Id); p != "" {
					peers[p] = true
				}
			}
		}
		members, _, err := c.GetChannelMembersForUser(ctx, me.Id, t.Id, "")
		if err != nil {
			return nil, fmt.Errorf("load channel memberships in team %s (%s): %w", t.Name, t.Id, err)
		}
		for i := range members {
			sb.Members = append(sb.Members, &members[i])
		}
		if sb.CRT {
			threads, _, err := c.GetUserThreads(ctx, me.Id, t.Id, model.GetUserThreadsOpts{PageSize: 50})
			if err != nil {
				// Без списка тредов каналами пользоваться можно — не валим сайдбар.
				m.log.Warn("load threads", "team", t.Name, "team_id", t.Id, "err", err)
				continue
			}
			sb.Threads = append(sb.Threads, threads.Threads...)
			for _, id := range threadPostAuthors(threads.Threads) {
				peers[id] = true
			}
		}
	}
	delete(peers, me.Id)
	ids := make([]string, 0, len(peers))
	for id := range peers {
		ids = append(ids, id)
	}
	if len(ids) > 0 {
		sb.Users, _, err = c.GetUsersByIds(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("load %d users for direct messages and threads: %w", len(ids), err)
		}
	}
	// Присутствие собеседников — для точек у личек, своё — для «не беспокоить»
	// (тогда без уведомлений). Не обязательно: без него сайдбар тоже работает.
	if sb.Statuses, _, err = c.GetUsersStatusesByIds(ctx, append(ids, me.Id)); err != nil {
		m.log.Warn("load statuses", "users", len(ids)+1, "err", err)
	}
	sb.Saved = m.savedIDs(ctx) // для меток «Сохранено»; не вышло — прежние

	return sb, nil
}

func channelPrefix(ch *model.Channel) string {
	switch ch.Type {
	case model.ChannelTypeOpen:
		return "# "
	case model.ChannelTypePrivate:
		return "🔒 "
	case model.ChannelTypeDirect:
		return "@"
	}
	return ""
}

// Канал и лента

func (m *MainWindow) openChannel(id string) {
	if id == threadsID {
		m.showThreads()
		return
	}
	if id == schedID {
		m.showScheduled()
		return
	}
	ch := m.st.Channel(id)
	if ch == nil {
		return
	}
	prev := m.current
	if prev == threadsID {
		prev = ""
	}
	m.current = id
	m.center.SetCurrentWidget(m.channelPage)
	m.composer.SwitchTo(id)
	m.composer.SetPlaceholder("Сообщение в " + channelPrefix(ch) + m.st.Title(ch))
	m.updateScheduledNotes()
	m.updateTypingNotes()
	m.refreshHeader()
	m.composer.SetEnabled(true)
	m.composer.Focus()
	f := m.feedFor(id)
	m.showFeed(f)
	// «Новые сообщения» — от прошлого прочтения: запоминаем до markViewed.
	f.SetNewSince(m.st.LastViewed(id))

	switch tl := m.st.Timeline(id); {
	case tl == nil:
		f.Loading()
		m.loadTimeline(id)
	case !tl.Loaded:
		f.Loading() // уже грузится
	case !f.ready:
		m.showTimeline(id, tl)
	case m.scrollTo != "":
		to := m.scrollTo
		m.scrollTo = ""
		m.reveal(to) // перешли по ссылке на сообщение
	}
	// Помеченный непрочитанным остаётся таким и когда из него уходят: не
	// передаём его серверу как прочитанный (prev). Вернулись в канал — прочитан.
	if m.manualUnread[prev] {
		delete(m.manualUnread, prev)
		prev = ""
	}
	delete(m.manualUnread, id)
	m.markViewed(id, prev)
	if m.listShown(listPinned) && m.search.listChannel != id {
		m.showList(listPinned) // закреплённые — открытого канала
	}
	if m.OnChannelOpened != nil {
		m.OnChannelOpened(id)
	}
}

// refreshHeader перерисовывает шапки канала и списка тредов.
func (m *MainWindow) refreshHeader() {
	p := Pal()
	m.threadsHead.set(iconPixmap("messages-square", p.Tx2, 16), "Треды", "Треды, на которые вы подписаны")
	ch := m.st.Channel(m.current)
	if ch == nil {
		m.head.set(nil, "Выберите канал", "")
		return
	}
	var icon *qt.QPixmap
	switch ch.Type {
	case model.ChannelTypeOpen:
		icon = iconPixmap("hash", p.Tx2, 16)
	case model.ChannelTypePrivate:
		icon = iconPixmap("lock", p.Tx2, 16)
	case model.ChannelTypeGroup:
		icon = iconPixmap("users", p.Tx2, 16)
	case model.ChannelTypeDirect:
		peer := m.st.DMPeer(ch)
		icon = avatarPixmap(m.assets, m.st.User(peer), m.st.Username(peer), false, 16, m.refreshHeader)
	}
	var sub []string
	if ch.Purpose != "" {
		sub = append(sub, html.EscapeString(ch.Purpose))
	}
	if ch.Header != "" {
		sub = append(sub, markdown.RenderInline(ch.Header, p))
	}
	m.head.set(icon, html.EscapeString(m.st.Title(ch)), strings.Join(sub, " · "))
}

// loadTimeline грузит последние посты. Пустая лента в store появляется сразу,
// чтобы посты из WebSocket, пришедшие во время загрузки, не потерялись.
// showTimeline показывает загруженную ленту канала или заглушку пустого канала.
func (m *MainWindow) showTimeline(id string, tl *store.Timeline) {
	f := m.feedFor(id)
	f.SetPosts(tl.Posts, tl.HasMore)
	f.ready = true
	if to := m.scrollTo; to != "" && id == m.current {
		m.scrollTo = ""
		m.reveal(to) // перешли по ссылке на сообщение
	}
	if len(tl.Posts) > 0 {
		return
	}
	ch := m.st.Channel(id)
	if ch == nil {
		return
	}
	iconName, text := "hash", ""
	switch ch.Type {
	case model.ChannelTypePrivate:
		iconName, text = "lock", "Канал приватный: сообщения видят только участники."
	case model.ChannelTypeDirect, model.ChannelTypeGroup:
		iconName = "message-circle"
	}
	f.Placeholder(iconName, "В канале «"+m.st.Title(ch)+"» пока нет сообщений", text)
	f.ready = true
}

func (m *MainWindow) loadTimeline(id string) { m.fetchTimeline(id, false, nil) }

// fetchTimeline грузит первую страницу ленты канала. Пустая Timeline ставится
// сразу: посты из WebSocket, пришедшие во время загрузки, не теряются.
// background — фоновая загрузка заранее: ошибка только в лог (откроют канал —
// загрузится обычным порядком). done — в главном потоке, когда закончили.
//
// Загруженная лента после обрыва связи (Stale) сверяется со свежей страницей:
// Timeline.Sync, а в ленте на экране меняется только изменившееся (Merge).
func (m *MainWindow) fetchTimeline(id string, background bool, done func()) {
	tl := m.st.Timeline(id)
	resync := tl != nil && tl.Loaded
	if resync {
		tl.Stale = false // уже сверяем: preload второй раз не попросит
	} else {
		tl = &store.Timeline{}
		m.st.SetTimeline(id, tl)
	}
	crt := m.st.CRT()
	finish := func() {
		if done != nil {
			done()
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		list, _, err := m.s.Client.GetPostsForChannel(ctx, id, 0, pageSize, "", crt, false)
		m.ui(func() {
			if m.st.Timeline(id) != tl {
				finish()
				return // ленты сбросили (переподключение), загрузку начнут заново
			}
			if err != nil && resync {
				tl.Stale = true // сверим после следующего переподключения
				m.log.Warn("resync timeline", "channel", id, "err", err)
				finish()
				return
			}
			if err != nil {
				m.st.SetTimeline(id, nil)
				err = fmt.Errorf("load posts of channel %s: %w", id, err)
				switch {
				case m.current == id:
					m.fail("load timeline", err)
					m.feed.Error(err.Error())
				case background:
					m.log.Warn("preload timeline", "err", err)
				default:
					m.fail("load timeline", err)
				}
				finish()
				return
			}
			posts := store.PostsOf(list)
			m.withUsers(postAuthors(posts), func() {
				defer finish()
				if m.st.Timeline(id) != tl {
					return
				}
				if resync {
					changed, removed := tl.Sync(posts, len(list.Order) >= pageSize)
					m.mergeFeed(id, changed, removed)
					return
				}
				tl.Prepend(posts, len(list.Order) >= pageSize)
				tl.Loaded = true
				m.showTimeline(id, tl) // и скрытой ленте: рисуется фоном
			})
		})
	}()
}

// preloadWorkers — сколько лент грузим заранее одновременно: быстро, но без
// залпа запросов на сервер.
const preloadWorkers = 3

// preload — последние страницы всех каналов и личек заранее, в фоне: переход в
// канал без ожидания, а дальше их держит свежими WebSocket. Сначала каналы с
// непрочитанным, потом лички по свежести, потом остальные. Зовётся после
// загрузки сайдбара — значит, и после переподключения: загруженные тогда
// сверяются с сервером (Stale).
func (m *MainWindow) preload() {
	var unread, direct, rest []string
	add := func(id string) {
		if u, _ := m.st.Unread(id); u {
			unread = append(unread, id)
		} else if ch := m.st.Channel(id); ch != nil && ch.IsGroupOrDirect() {
			direct = append(direct, id)
		} else {
			rest = append(rest, id)
		}
	}
	for _, ch := range m.st.DirectChannels() {
		add(ch.Id)
	}
	for _, t := range m.st.Teams() {
		for _, ch := range m.st.TeamChannels(t.Id) {
			add(ch.Id)
		}
	}
	m.preloadQueue = append(append(unread, direct...), rest...)
	for m.preloading < preloadWorkers && m.preloadNext() {
	}
}

// preloadNext — следующую ленту из очереди; false — грузить больше нечего.
func (m *MainWindow) preloadNext() bool {
	for len(m.preloadQueue) > 0 && !m.closed {
		id := m.preloadQueue[0]
		m.preloadQueue = m.preloadQueue[1:]
		if tl := m.st.Timeline(id); (tl != nil && !tl.Stale) || m.st.Channel(id) == nil {
			continue // уже загружена или грузится, канала больше нет
		}
		m.preloading++
		m.fetchTimeline(id, true, func() {
			m.preloading--
			m.preloadNext()
		})
		return true
	}
	return false
}

func (m *MainWindow) loadOlder() { m.loadOlderThen(nil) }

// loadOlderThen подгружает страницу постов старше загруженных; done — когда
// они в ленте (если ничего не вышло — не зовётся).
func (m *MainWindow) loadOlderThen(done func()) {
	id := m.current
	tl := m.st.Timeline(id)
	if tl == nil || !tl.Loaded || tl.Oldest() == nil {
		m.feed.OlderFailed()
		return
	}
	oldest := tl.Oldest().Id
	crt := m.st.CRT()
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		list, _, err := m.s.Client.GetPostsBefore(ctx, id, oldest, 0, pageSize, "", crt, false)
		m.ui(func() {
			if err != nil {
				m.fail("load older posts", fmt.Errorf("load posts before %s in channel %s: %w", oldest, id, err))
				if f := m.feeds[id]; f != nil {
					f.OlderFailed()
				}
				return
			}
			posts := store.PostsOf(list)
			m.withUsers(postAuthors(posts), func() {
				if m.st.Timeline(id) != tl {
					return
				}
				added := tl.Prepend(posts, len(list.Order) >= pageSize)
				// И если уже ушли из канала: лента и store не должны разойтись.
				if f := m.feeds[id]; f != nil {
					f.PrependOlder(added, tl.HasMore)
				}
				if done != nil {
					done()
				}
			})
		})
	}()
}

func postAuthors(posts []*model.Post) []string {
	ids := make([]string, len(posts))
	for i, p := range posts {
		ids[i] = p.UserId
	}
	return ids
}

// markViewed отмечает канал прочитанным локально и на сервере.
func (m *MainWindow) markViewed(id, prev string) {
	m.st.MarkViewed(id)
	m.refreshItem(id)
	view := &model.ChannelView{ChannelId: id, PrevChannelId: prev, CollapsedThreadsSupported: m.st.CRT()}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		_, _, err := m.s.Client.ViewChannel(ctx, m.s.Me.Id, view)
		if err != nil {
			m.log.Warn("view channel", "channel", id, "err", err)
		}
	}()
}

func (m *MainWindow) send(text string, fileIDs []string) {
	id := m.current
	if m.st.Channel(id) == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		p, _, err := m.s.Client.CreatePost(ctx, &model.Post{ChannelId: id, Message: text, FileIds: fileIDs})
		m.ui(func() {
			if err != nil {
				m.fail("send", fmt.Errorf("send to channel %s: %w", id, err))
				m.composer.Restore()
				return
			}
			m.composer.Sent()
			// Тот же пост придёт и по WebSocket — applyPosted не задублирует.
			m.applyPosted(store.NewPost{Post: p})
		})
	}()
}

// applyPosted — новый пост из WebSocket или из ответа на отправку.
func (m *MainWindow) applyPosted(e store.NewPost) {
	p := e.Post
	m.stopTyping(p)
	r := m.st.ApplyPosted(p, e.Mentioned)
	m.applyToThread(p)
	m.maybeNotify(e)
	if !r.KnownChannel {
		m.reloadSidebar() // новая личка или канал, куда нас только что добавили
		return
	}
	if ch := m.st.Channel(p.ChannelId); ch.IsGroupOrDirect() {
		m.fillSidebar() // личные отсортированы по свежести
	} else {
		m.refreshItem(p.ChannelId)
	}
	if unread, _ := m.st.Unread(p.ChannelId); unread && p.ChannelId == m.current && m.w.IsActiveWindow() && !m.manualUnread[p.ChannelId] {
		m.markViewed(p.ChannelId, "")
	}
	// В ленту своего канала — и скрытую: она готова к показу.
	tl, f := m.st.Timeline(p.ChannelId), m.feeds[p.ChannelId]
	if f == nil || tl == nil || !tl.Loaded {
		return
	}
	if r.Root != nil && f.Has(r.Root.Id) {
		f.Upsert(r.Root) // счётчик ответов под корнем
	}
	if r.Timeline {
		m.withUsers([]string{p.UserId}, func() {
			if m.st.Timeline(p.ChannelId) == tl {
				f.Upsert(p)
			}
		})
	}
}

// Realtime: методы mm.RealtimeHandler, зовутся из горутины RunRealtime.

func (m *MainWindow) OnConnected() {
	m.ui(func() {
		m.online = true
		m.setConnection(ConnOnline)
		m.banner.hide()
		m.composer.SetOffline(false)
		m.panel.composer.SetOffline(false)
		if !m.connectedOnce {
			m.connectedOnce = true // сайдбар уже грузится из Start
			return
		}
		// События за время обрыва потеряны: загруженные ленты сверяем с
		// сервером (открытую — первой), остальные — preload после сайдбара.
		m.st.MarkTimelinesStale()
		m.drafts.reload = true // черновики из других клиентов за время обрыва
		if id := m.current; m.st.Channel(id) != nil {
			if tl := m.st.Timeline(id); tl == nil || !tl.Loaded {
				m.feed.Loading()
			}
			m.loadTimeline(id)
		}
		m.reloadSidebar()
		m.assets.Retry()
		m.sched.reload()
		if t := m.thread; t != nil {
			m.openThread(t.rootID)
		}
	})
}

func (m *MainWindow) OnDisconnected(err error, retryIn time.Duration) {
	m.log.Warn("realtime disconnected", "err", err, "retry_in", retryIn)
	m.ui(func() {
		m.online = false
		m.setConnection(ConnReconnecting)
		m.banner.show(retryIn)
		m.composer.SetOffline(true)
		m.panel.composer.SetOffline(true)
	})
}

func (m *MainWindow) OnEvent(ev *model.WebSocketEvent) {
	m.log.Debug("ws event", "type", ev.EventType())
	h, err := m.decodeEvent(ev)
	if err != nil {
		m.log.Warn("ws event", "type", ev.EventType(), "err", err)
		return
	}
	if h != nil {
		m.ui(h)
	}
}
