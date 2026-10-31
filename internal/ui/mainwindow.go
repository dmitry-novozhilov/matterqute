package ui

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"matterqute/internal/markdown"
	"matterqute/internal/mm"
	"matterqute/internal/store"
)

// Роль данных элемента сайдбара: id канала (у заголовков групп пусто).
const channelIDRole = int(qt.UserRole) + 1

// Сколько постов грузить за раз.
const pageSize = 60

// MainWindow — окно одного сервера. Все методы — из главного потока Qt;
// горутины возвращаются в него через ui(), который проверяет closed.
type MainWindow struct {
	w      *qt.QMainWindow
	s      *mm.Session
	st     *store.Store
	log    *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
	closed bool

	sidebar     *qt.QTreeWidget
	items       map[string]*qt.QTreeWidgetItem // по id канала; threadsID — «Треды»
	center      *qt.QStackedWidget             // channelPage или threadsPage
	channelPage *qt.QWidget
	title       *qt.QLabel
	feed        *Feed
	composer    *Composer
	threadsPage *qt.QWidget
	threadsList *qt.QListWidget
	panel       *ThreadPanel
	thread      *threadView // открытый в панели тред; nil — панель закрыта
	conn        *qt.QLabel

	current        string // открытый канал или threadsID
	initialChannel string // открыть после первой загрузки сайдбара
	sidebarLoading bool
	sidebarDirty   bool // пока грузили, пришло событие, требующее перезагрузки
	connectedOnce  bool

	OnLogout        func()
	OnChannelOpened func(id string)
}

func NewMainWindow(s *mm.Session, log *slog.Logger, initialChannel string) *MainWindow {
	ctx, cancel := context.WithCancel(context.Background())
	m := &MainWindow{
		w:      qt.NewQMainWindow2(),
		s:      s,
		st:     store.New(s.Me),
		log:    log.With("server", s.ServerURL),
		ctx:    ctx,
		cancel: cancel,
		items:  map[string]*qt.QTreeWidgetItem{},

		initialChannel: initialChannel,
	}
	m.w.SetWindowTitle(fmt.Sprintf("matterqute — %s", strings.TrimPrefix(strings.TrimPrefix(s.ServerURL, "https://"), "http://")))
	m.w.Resize(1100, 720)
	m.w.SetAttribute(qt.WA_DeleteOnClose)
	m.w.OnCloseEvent(func(super func(*qt.QCloseEvent), ev *qt.QCloseEvent) {
		m.closed = true
		m.cancel()
		super(ev)
	})
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

	m.buildMenu()

	m.sidebar = qt.NewQTreeWidget2()
	m.sidebar.SetHeaderHidden(true)
	m.sidebar.SetMinimumWidth(220)
	m.sidebar.OnCurrentItemChanged(func(cur, _ *qt.QTreeWidgetItem) {
		if cur == nil {
			return
		}
		if id := cur.Data(0, channelIDRole).ToString(); id != "" && id != m.current {
			m.openChannel(id)
		}
	})

	m.title = newTextLabel(qt.RichText)
	m.title.SetOpenExternalLinks(true)
	m.title.SetMargin(10)
	m.title.SetText("Выберите канал")

	m.feed = NewFeed(m.st.Username, s.Me.Id, false)
	m.feed.OnNeedOlder = m.loadOlder
	m.feed.OnOpenThread = m.openThread
	m.feed.OnToggleReaction = m.toggleReaction

	m.composer = NewComposer()
	m.composer.OnSend = m.send
	m.composer.SetEnabled(false)

	m.channelPage = qt.NewQWidget2()
	cl := qt.NewQVBoxLayout(m.channelPage)
	cl.SetContentsMargins(0, 0, 0, 0)
	cl.SetSpacing(0)
	cl.AddWidget(m.title.QWidget)
	cl.AddWidget2(m.feed.Widget(), 1)
	cl.AddWidget(m.composer.Widget())

	m.threadsPage = qt.NewQWidget2()
	tl := qt.NewQVBoxLayout(m.threadsPage)
	tl.SetContentsMargins(0, 0, 0, 0)
	threadsTitle := newTextLabel(qt.RichText)
	threadsTitle.SetMargin(10)
	threadsTitle.SetText("<b>Треды</b>")
	m.threadsList = qt.NewQListWidget2()
	m.threadsList.SetWordWrap(true)
	m.threadsList.SetSpacing(4)
	m.threadsList.OnItemClicked(func(it *qt.QListWidgetItem) {
		if id := it.Data(threadIDRole).ToString(); id != "" {
			m.openThread(id)
		}
	})
	tl.AddWidget(threadsTitle.QWidget)
	tl.AddWidget2(m.threadsList.QWidget, 1)

	m.center = qt.NewQStackedWidget2()
	m.center.AddWidget(m.channelPage)
	m.center.AddWidget(m.threadsPage)

	m.panel = NewThreadPanel(m.st.Username, s.Me.Id, m.closeThread)
	m.panel.feed.OnToggleReaction = m.toggleReaction
	m.panel.composer.OnSend = m.sendReply

	split := qt.NewQSplitter2()
	split.AddWidget(m.sidebar.QWidget)
	split.AddWidget(m.center.QWidget)
	split.AddWidget(m.panel.w)
	split.SetStretchFactor(1, 1)
	split.SetSizes([]int{260, 840, 400})
	m.w.SetCentralWidget(split.QWidget)

	m.conn = qt.NewQLabel3("подключаемся…")
	m.w.StatusBar().AddWidget(qt.NewQLabel3(fmt.Sprintf("@%s", s.Me.Username)).QWidget)
	m.w.StatusBar().AddPermanentWidget(m.conn.QWidget)
	return m
}

func (m *MainWindow) Show() {
	m.w.Show()
	m.reloadSidebar()
	go m.s.RunRealtime(m.ctx, m)
}

// Close закрывает окно без выхода из аккаунта.
func (m *MainWindow) Close() { m.w.Close() }

func (m *MainWindow) buildMenu() {
	file := m.w.MenuBar().AddMenuWithTitle("&Файл")
	logout := file.QWidget.AddActionWithText("Выйти из аккаунта")
	logout.OnTriggered(func() {
		if m.OnLogout != nil {
			m.OnLogout()
		}
	})
	file.AddSeparator()
	quit := file.QWidget.AddActionWithText("Выход")
	quit.SetShortcut(qt.NewQKeySequence2("Ctrl+Q"))
	quit.OnTriggered(qt.QCoreApplication_Quit)
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
	m.w.StatusBar().ShowMessage2(err.Error(), 15000)
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
				m.st.SetSidebar(sb)
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
	if sb.CRT, err = m.s.FetchCRT(ctx); err != nil {
		m.log.Warn("detect collapsed threads, assume off", "err", err)
	}
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
	if len(peers) > 0 {
		ids := make([]string, 0, len(peers))
		for id := range peers {
			ids = append(ids, id)
		}
		sb.Users, _, err = c.GetUsersByIds(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("load %d users for direct messages and threads: %w", len(ids), err)
		}
	}
	return sb, nil
}

func (m *MainWindow) fillSidebar() {
	m.sidebar.Clear()
	m.items = map[string]*qt.QTreeWidgetItem{}
	var toSelect *qt.QTreeWidgetItem
	if m.st.CRT() {
		it := qt.NewQTreeWidgetItem2([]string{""})
		it.SetData(0, channelIDRole, qt.NewQVariant11(threadsID))
		m.sidebar.AddTopLevelItem(it)
		m.items[threadsID] = it
		m.refreshItem(threadsID)
		if m.current == threadsID {
			toSelect = it
		}
	}
	addGroup := func(title string, chs []*model.Channel) {
		if len(chs) == 0 {
			return
		}
		group := qt.NewQTreeWidgetItem2([]string{title})
		group.SetFlags(qt.ItemIsEnabled)
		m.sidebar.AddTopLevelItem(group)
		for _, ch := range chs {
			it := qt.NewQTreeWidgetItem6(group)
			it.SetData(0, channelIDRole, qt.NewQVariant11(ch.Id))
			m.items[ch.Id] = it
			m.refreshItem(ch.Id)
			if ch.Id == m.current {
				toSelect = it
			}
		}
		group.SetExpanded(true)
	}
	for _, t := range m.st.Teams() {
		addGroup(t.DisplayName, m.st.TeamChannels(t.Id))
	}
	addGroup("Личные сообщения", m.st.DirectChannels())
	if m.current == "" && m.initialChannel != "" {
		toSelect = m.items[m.initialChannel]
		m.initialChannel = ""
	}
	if toSelect != nil {
		m.sidebar.SetCurrentItem(toSelect)
	}
}

// refreshItem перерисовывает строку канала: имя, жирность непрочитанного, упоминания.
func (m *MainWindow) refreshItem(channelID string) {
	it := m.items[channelID]
	if it == nil {
		return
	}
	var text string
	var unread bool
	var mentions int64
	if channelID == threadsID {
		text = "💬 Треды"
		unread, mentions = m.st.ThreadsUnread()
	} else {
		ch := m.st.Channel(channelID)
		if ch == nil {
			return
		}
		unread, mentions = m.st.Unread(channelID)
		text = channelPrefix(ch) + m.st.Title(ch)
	}
	if mentions > 0 {
		text += fmt.Sprintf("  (%d)", mentions)
	}
	it.SetText(0, text)
	font := it.Font(0)
	font.SetBold(unread || mentions > 0)
	it.SetFont(0, font)
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
	m.title.SetText(channelTitleHTML(ch, m.st.Title(ch)))
	m.composer.SetEnabled(true)
	m.composer.Focus()

	switch tl := m.st.Timeline(id); {
	case tl == nil:
		m.feed.Reset("Загрузка…")
		m.loadTimeline(id)
	case tl.Loaded:
		m.feed.SetPosts(tl.Posts, tl.HasMore)
	default:
		m.feed.Reset("Загрузка…") // уже грузится
	}
	m.markViewed(id, prev)
	if m.OnChannelOpened != nil {
		m.OnChannelOpened(id)
	}
}

func channelTitleHTML(ch *model.Channel, title string) string {
	s := "<b>" + html.EscapeString(channelPrefix(ch)+title) + "</b>"
	if ch.Purpose != "" {
		s += `<br><span style="color:gray">` + markdown.Render(ch.Purpose) + "</span>"
	}
	if ch.Header != "" {
		s += "<br>" + markdown.Render(ch.Header)
	}
	return s
}

// loadTimeline грузит последние посты. Пустая лента в store появляется сразу,
// чтобы посты из WebSocket, пришедшие во время загрузки, не потерялись.
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
					m.feed.Reset(err.Error())
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
					m.feed.SetPosts(tl.Posts, tl.HasMore)
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

func (m *MainWindow) send(text string) {
	id := m.current
	if id == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		p, _, err := m.s.Client.CreatePost(ctx, &model.Post{ChannelId: id, Message: text})
		m.ui(func() {
			if err != nil {
				m.fail("send", fmt.Errorf("send to channel %s: %w", id, err))
				if m.current == id {
					m.composer.Restore(text)
				}
				return
			}
			// Тот же пост придёт и по WebSocket — applyPosted не задублирует.
			m.applyPosted(p, false)
		})
	}()
}

// applyPosted — новый пост из WebSocket или из ответа на отправку.
func (m *MainWindow) applyPosted(p *model.Post, mentioned bool) {
	r := m.st.ApplyPosted(p, mentioned)
	m.applyToThread(p)
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
		m.conn.SetText("в сети")
		if !m.connectedOnce {
			m.connectedOnce = true // сайдбар уже грузится из Show
			return
		}
		// События за время обрыва потеряны: перечитываем всё, что показываем.
		m.st.DropTimelines()
		m.reloadSidebar()
		if t := m.thread; t != nil {
			m.openThread(t.rootID)
		}
		if id := m.current; m.st.Channel(id) != nil {
			m.feed.Reset("Загрузка…")
			m.loadTimeline(id)
		}
	})
}

func (m *MainWindow) OnDisconnected(err error) {
	m.log.Warn("realtime disconnected", "err", err)
	m.ui(func() { m.conn.SetText("нет соединения, переподключаемся…") })
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
