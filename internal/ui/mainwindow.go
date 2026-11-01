package ui

import (
	"context"
	"fmt"
	"html"
	"log/slog"
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

	split       *qt.QSplitter
	columns     columns    // ширины сайдбара и панели треда
	saveColumns *qt.QTimer // сохранить ширины, когда разделитель отпустили
	sidebar     *qt.QTreeWidget
	items       map[string]*qt.QTreeWidgetItem // по id канала; threadsID — «Треды»
	center      *qt.QStackedWidget             // channelPage или threadsPage
	channelPage *qt.QWidget
	head        *header
	banner      *banner
	feed        *Feed
	composer    *Composer
	threadsPage *qt.QWidget
	threadsHead *header
	threadsList *qt.QListWidget
	panel       *ThreadPanel
	thread      *threadView // открытый в панели тред; nil — панель закрыта
	assets      *Assets
	notifier    Notifier           // nil — уведомлений нет (нет шины или сервера уведомлений)
	notes       map[string]uint32  // показанное уведомление по каналу или треду: следующее его заменит
	players     map[string]*player // открытые плееры по id файла

	suggestTimer   *qt.QTimer // запрос подсказок к серверу — после паузы в наборе
	suggestPending func()

	scrollTo     string       // после загрузки ленты прокрутить к этому посту (ссылка на сообщение)
	dndEnd       int64        // свой конец «не беспокоить», секунды; 0 — неизвестен или без срока
	statusPanel  *card        // последняя открытая панель статуса (для тестов)
	statusDialog *dialog      // последнее открытое окно своего статуса
	picker       *emojiPicker // последнее открытое окно выбора эмодзи
	recent       emoji.Recent // недавние эмодзи (настройка recent_emojis)
	customEmoji  []string     // имена кастомных эмодзи сервера
	emojiLoaded  bool
	mentions     int64 // последнее отданное в OnMentions; -1 — ещё не отдавали
	retryNow     chan struct{}
	unsubscribe  func()

	current         string // открытый канал или threadsID
	initialChannel  string // открыть после первой загрузки сайдбара
	selectAfterLoad string // открыть после перезагрузки сайдбара (новая личка)
	sidebarLoading  bool
	sidebarDirty    bool // пока грузили, пришло событие, требующее перезагрузки
	connectedOnce   bool
	online          bool

	OnChannelOpened  func(id string)
	OnConnection     func(ConnState)
	OnMessage        func(text string)                // короткое сообщение в строке состояния окна
	OnColumnsChanged func(sidebar, thread int)        // пользователь передвинул разделитель
	OnRaise          func()                           // показать окно поверх остальных (клик по уведомлению)
	OnMentions       func(n int64)                    // изменилось число ждущих упоминаний (трей)
	OnOwnStatus      func(pm *qt.QPixmap, tip string) // свой статус: эмодзи и подсказка, nil — нет
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
		w:        qt.NewQWidget2(),
		s:        s,
		st:       store.New(s.Me),
		log:      log.With("server", s.ServerURL),
		ctx:      ctx,
		cancel:   cancel,
		items:    map[string]*qt.QTreeWidgetItem{},
		notes:    map[string]uint32{},
		players:  map[string]*player{},
		mentions: -1,
		retryNow: make(chan struct{}, 1),

		initialChannel: initialChannel,
	}
	m.assets = NewAssets(cache.New(cacheDir, s.ServerURL, s.Client), ctx, m.ui, m.log)
	m.w.Resize(1280, 774)
	m.w.OnEvent(func(super func(*qt.QEvent) bool, ev *qt.QEvent) bool {
		if ev.Type() == qt.QEvent__WindowActivate {
			// Пока окно было неактивно, в открытом могло накопиться непрочитанное.
			if unread, _ := m.st.Unread(m.current); unread {
				m.markViewed(m.current, "")
			}
			if t := m.thread; t != nil && t.channelID != "" {
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

	deps := FeedDeps{Names: m.st.Username, Users: m.st.User, Me: s.Me, Assets: m.assets, ChannelExists: m.channelExists,
		LinkPreviews: m.st.LinkPreviews}
	m.feed = NewFeed(deps)
	m.feed.OnNeedOlder = m.loadOlder
	m.feed.OnOpenThread = m.openThread
	m.wireFeed(m.feed)

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
	cl.AddWidget2(m.feed.Widget(), 1)
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

	m.center = qt.NewQStackedWidget2()
	m.center.AddWidget(m.channelPage)
	m.center.AddWidget(m.threadsPage)
	m.center.SetMinimumWidth(360)

	deps.Thread = true
	m.panel = NewThreadPanel(deps, m.closeThread)
	m.wireFeed(m.panel.feed)
	m.panel.composer.OnSend = m.sendReply
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
	m.split.SetStretchFactor(1, 1)
	m.sidebar.SetMinimumWidth(sidebarMin)
	m.sidebar.SetMaximumWidth(sidebarMax)
	m.panel.w.SetMinimumWidth(threadMin)
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
		if m.panel.w.IsVisible() {
			m.columns.thread = sizes[2]
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
	m.closed = true
	m.cancel()
	// Все подписки на тему — до удаления виджетов: смена темы после выхода
	// из аккаунта иначе полезет в удалённые объекты.
	m.unsubscribe()
	m.panel.dispose()
	m.feed.dispose()
	m.composer.dispose()
	for _, pl := range m.players {
		pl.w.Close()
	}
	m.assets.stopAnimations()
	m.w.Close()
}

// restyle — смена темы: иконки и цвета, нарисованные кодом.
func (m *MainWindow) restyle() {
	m.banner.restyle()
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

// ui запускает f в главном потоке, если окно ещё живо.
func (m *MainWindow) ui(f func()) {
	mainthread.Start(func() {
		if !m.closed {
			f()
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
				if previewsChanged && !crtChanged {
					m.feed.renderAll()
					m.panel.feed.renderAll()
				}
				if crtChanged {
					// Ленты загружены в другом режиме (с ответами или без).
					m.st.DropTimelines()
					m.thread = nil
					m.panel.w.SetVisible(false)
					if ch := m.st.Channel(m.current); ch != nil {
						// fillSidebar выделит его заново, и он откроется в новом режиме.
						m.current, m.initialChannel = "", ch.Id
					}
				}
				m.fillSidebar()
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
	sb.CRT, sb.MaxFileSize, sb.LinkPreviews = settings.CRT, settings.MaxFileSize, settings.LinkPreviews
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
	m.refreshHeader()
	m.composer.SetEnabled(true)
	m.composer.Focus()
	m.feed.OnRetry = func() {
		m.feed.Loading()
		m.loadTimeline(id)
	}
	// «Новые сообщения» — от прошлого прочтения: запоминаем до markViewed.
	m.feed.SetNewSince(m.st.LastViewed(id))

	switch tl := m.st.Timeline(id); {
	case tl == nil:
		m.feed.Loading()
		m.loadTimeline(id)
	case tl.Loaded:
		m.showTimeline(id, tl)
	default:
		m.feed.Loading() // уже грузится
	}
	m.markViewed(id, prev)
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
	m.feed.SetPosts(tl.Posts, tl.HasMore)
	if to := m.scrollTo; to != "" {
		m.scrollTo = ""
		if m.feed.Has(to) {
			m.feed.ScrollToPost(to) // перешли по ссылке на сообщение
		}
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
	m.feed.Placeholder(iconName, "В канале «"+m.st.Title(ch)+"» пока нет сообщений", text)
}

func (m *MainWindow) loadTimeline(id string) {
	tl := &store.Timeline{}
	m.st.SetTimeline(id, tl)
	crt := m.st.CRT()
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		list, _, err := m.s.Client.GetPostsForChannel(ctx, id, 0, pageSize, "", crt, false)
		m.ui(func() {
			if m.st.Timeline(id) != tl {
				return // ленты сбросили (переподключение), загрузку начнут заново
			}
			if err != nil {
				m.st.SetTimeline(id, nil)
				err = fmt.Errorf("load posts of channel %s: %w", id, err)
				m.fail("load timeline", err)
				if m.current == id {
					m.feed.Error(err.Error())
				}
				return
			}
			posts := store.PostsOf(list)
			m.withUsers(postAuthors(posts), func() {
				if m.st.Timeline(id) != tl {
					return
				}
				tl.Prepend(posts, len(list.Order) >= pageSize)
				tl.Loaded = true
				if m.current == id {
					m.showTimeline(id, tl)
				}
			})
		})
	}()
}

func (m *MainWindow) loadOlder() {
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
				if m.current == id {
					m.feed.OlderFailed()
				}
				return
			}
			posts := store.PostsOf(list)
			m.withUsers(postAuthors(posts), func() {
				if m.st.Timeline(id) != tl {
					return
				}
				added := tl.Prepend(posts, len(list.Order) >= pageSize)
				if m.current == id {
					m.feed.PrependOlder(added, tl.HasMore)
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
	if p.ChannelId != m.current {
		return
	}
	tl := m.st.Timeline(p.ChannelId)
	if r.Root != nil && tl.Loaded && m.feed.Has(r.Root.Id) {
		m.feed.Upsert(r.Root) // счётчик ответов под корнем
	}
	if unread, _ := m.st.Unread(p.ChannelId); unread && m.w.IsActiveWindow() {
		m.markViewed(p.ChannelId, "")
	}
	if r.Timeline && tl.Loaded {
		m.withUsers([]string{p.UserId}, func() {
			if m.current == p.ChannelId && m.st.Timeline(p.ChannelId) == tl {
				m.feed.Upsert(p)
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
		// События за время обрыва потеряны: перечитываем всё, что показываем.
		m.st.DropTimelines()
		m.reloadSidebar()
		m.assets.Retry()
		if t := m.thread; t != nil {
			m.openThread(t.rootID)
		}
		if id := m.current; m.st.Channel(id) != nil {
			m.feed.Loading()
			m.loadTimeline(id)
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
