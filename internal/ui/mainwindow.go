package ui

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"matterqute/internal/mm"
)

// Роль данных элемента сайдбара: id канала (у заголовков команд пусто).
const channelIDRole = int(qt.UserRole) + 1

// MainWindow — окно одного сервера. Все методы — из главного потока Qt;
// горутины возвращаются в него через mainthread.Start и проверяют closed.
type MainWindow struct {
	w       *qt.QMainWindow
	s       *mm.Session
	log     *slog.Logger
	ctx     context.Context
	cancel  context.CancelFunc
	closed  bool
	loading bool

	sidebar  *qt.QTreeWidget
	header   *qt.QLabel
	conn     *qt.QLabel
	channels map[string]*model.Channel

	connectedOnce bool

	OnLogout func()
}

func NewMainWindow(s *mm.Session, log *slog.Logger) *MainWindow {
	ctx, cancel := context.WithCancel(context.Background())
	m := &MainWindow{
		w:        qt.NewQMainWindow2(),
		s:        s,
		log:      log.With("server", s.ServerURL),
		ctx:      ctx,
		cancel:   cancel,
		channels: map[string]*model.Channel{},
	}
	m.w.SetWindowTitle(fmt.Sprintf("matterqute — %s", strings.TrimPrefix(strings.TrimPrefix(s.ServerURL, "https://"), "http://")))
	m.w.Resize(1100, 720)
	m.w.SetAttribute(qt.WA_DeleteOnClose)
	m.w.OnCloseEvent(func(super func(*qt.QCloseEvent), ev *qt.QCloseEvent) {
		m.shutdown()
		super(ev)
	})

	m.buildMenu()

	m.sidebar = qt.NewQTreeWidget2()
	m.sidebar.SetHeaderHidden(true)
	m.sidebar.SetMinimumWidth(220)
	m.sidebar.OnCurrentItemChanged(func(cur, _ *qt.QTreeWidgetItem) {
		if cur != nil {
			m.selectChannel(cur.Data(0, channelIDRole).ToString())
		}
	})

	m.header = qt.NewQLabel3("Выберите канал")
	m.header.SetWordWrap(true)
	m.header.SetTextFormat(qt.PlainText)
	m.header.SetTextInteractionFlags(qt.TextSelectableByMouse)
	m.header.SetAlignment(qt.AlignTop | qt.AlignLeft)
	m.header.SetMargin(12)

	split := qt.NewQSplitter2()
	split.AddWidget(m.sidebar.QWidget)
	split.AddWidget(m.header.QWidget)
	split.SetStretchFactor(1, 1)
	split.SetSizes([]int{260, 840})
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

func (m *MainWindow) shutdown() {
	m.closed = true
	m.cancel()
}

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

// RealtimeHandler — зовётся из горутины RunRealtime.

func (m *MainWindow) OnConnected() {
	m.ui(func() {
		m.conn.SetText("в сети")
		// Первый hello приходит сразу после старта, сайдбар уже грузится;
		// после переподключения пропущенные события восполняем перезагрузкой.
		if m.connectedOnce {
			m.reloadSidebar()
		}
		m.connectedOnce = true
	})
}

func (m *MainWindow) OnDisconnected(err error) {
	m.log.Warn("realtime disconnected", "err", err)
	m.ui(func() { m.conn.SetText("нет соединения, переподключаемся…") })
}

func (m *MainWindow) OnEvent(ev *model.WebSocketEvent) {
	m.log.Debug("ws event", "type", ev.EventType())
}

type sidebarData struct {
	teams    []*model.Team
	channels map[string][]*model.Channel // по id команды; личные — по ""
	users    map[string]*model.User      // собеседники в личных
}

func (m *MainWindow) reloadSidebar() {
	if m.loading {
		return
	}
	m.loading = true
	go func() {
		data, err := m.fetchSidebar()
		m.ui(func() {
			m.loading = false
			if err != nil {
				m.log.Error("load sidebar", "err", err)
				m.w.StatusBar().ShowMessage(err.Error())
				return
			}
			m.fillSidebar(data)
		})
	}()
}

func (m *MainWindow) fetchSidebar() (*sidebarData, error) {
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	defer cancel()
	c, me := m.s.Client, m.s.Me.Id

	teams, _, err := c.GetTeamsForUser(ctx, me, "")
	if err != nil {
		return nil, fmt.Errorf("load teams for %s: %w", m.s.Me.Username, err)
	}
	d := &sidebarData{teams: teams, channels: map[string][]*model.Channel{}, users: map[string]*model.User{}}
	direct := map[string]*model.Channel{}
	for _, t := range teams {
		chs, _, err := c.GetChannelsForTeamForUser(ctx, t.Id, me, false, "")
		if err != nil {
			return nil, fmt.Errorf("load channels of team %s (%s): %w", t.Name, t.Id, err)
		}
		for _, ch := range chs {
			if ch.IsGroupOrDirect() {
				direct[ch.Id] = ch // приходят в ответе каждой команды
			} else {
				d.channels[t.Id] = append(d.channels[t.Id], ch)
			}
		}
	}

	d.users[me] = m.s.Me
	var peers []string
	for _, ch := range direct {
		d.channels[""] = append(d.channels[""], ch)
		if p := dmPeer(ch, me); ch.Type == model.ChannelTypeDirect && p != me {
			peers = append(peers, p)
		}
	}
	if len(peers) > 0 {
		users, _, err := c.GetUsersByIds(ctx, peers)
		if err != nil {
			return nil, fmt.Errorf("load %d direct message peers: %w", len(peers), err)
		}
		for _, u := range users {
			d.users[u.Id] = u
		}
	}
	return d, nil
}

func (m *MainWindow) fillSidebar(d *sidebarData) {
	selected := ""
	if cur := m.sidebar.CurrentItem(); cur != nil {
		selected = cur.Data(0, channelIDRole).ToString()
	}
	m.sidebar.Clear()
	m.channels = map[string]*model.Channel{}

	var toSelect *qt.QTreeWidgetItem
	addGroup := func(title string, chs []*model.Channel) {
		if len(chs) == 0 {
			return
		}
		group := qt.NewQTreeWidgetItem2([]string{title})
		group.SetFlags(qt.ItemIsEnabled)
		m.sidebar.AddTopLevelItem(group)
		names := make(map[string]string, len(chs))
		for _, ch := range chs {
			names[ch.Id] = channelTitle(ch, m.s.Me.Id, d.users)
		}
		slices.SortFunc(chs, func(a, b *model.Channel) int {
			return cmp.Compare(strings.ToLower(names[a.Id]), strings.ToLower(names[b.Id]))
		})
		for _, ch := range chs {
			m.channels[ch.Id] = ch
			it := qt.NewQTreeWidgetItem7(group, []string{names[ch.Id]})
			it.SetData(0, channelIDRole, qt.NewQVariant11(ch.Id))
			if ch.Id == selected {
				toSelect = it
			}
		}
		group.SetExpanded(true)
	}
	for _, t := range d.teams {
		addGroup(t.DisplayName, d.channels[t.Id])
	}
	addGroup("Личные сообщения", d.channels[""])
	if toSelect != nil {
		m.sidebar.SetCurrentItem(toSelect)
	}
}

func channelTitle(ch *model.Channel, me string, users map[string]*model.User) string {
	switch ch.Type {
	case model.ChannelTypeOpen:
		return "# " + ch.DisplayName
	case model.ChannelTypePrivate:
		return "🔒 " + ch.DisplayName
	case model.ChannelTypeDirect:
		peer := dmPeer(ch, me)
		name := peer
		if u := users[peer]; u != nil {
			name = u.Username
		}
		if peer == me {
			return "@" + name + " (вы)"
		}
		return "@" + name
	}
	return ch.DisplayName // групповые: сервер сам кладёт сюда список участников
}

// dmPeer — собеседник в личном канале; для канала с самим собой — me
// (GetOtherUserIdForDM в этом случае возвращает "").
func dmPeer(ch *model.Channel, me string) string {
	if id := ch.GetOtherUserIdForDM(me); id != "" {
		return id
	}
	return me
}

func (m *MainWindow) selectChannel(id string) {
	ch := m.channels[id]
	if ch == nil {
		return
	}
	text := ch.DisplayName
	if ch.Purpose != "" {
		text += "\n\n" + ch.Purpose
	}
	if ch.Header != "" {
		text += "\n\n" + ch.Header
	}
	m.header.SetText(text)
}
