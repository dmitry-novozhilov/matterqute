package ui

import (
	"fmt"
	"slices"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/status"
	"matterqute/internal/store"
)

// Роли данных строк сайдбара.
const (
	channelIDRole = int(qt.UserRole) + 1 // id канала или threadsID; у групп пусто
	groupRole     = int(qt.UserRole) + 4 // подпись группы (категория; без категорий — команда, «Личные сообщения»)
	teamRole      = int(qt.UserRole) + 5 // id команды у её группы
	categoryRole  = int(qt.UserRole) + 7 // id категории у группы; пусто — категорий с сервера нет
	teamLabelRole = int(qt.UserRole) + 8 // название команды у строки-заголовка (при нескольких командах)
	collapsedRole = int(qt.UserRole) + 9 // группа свёрнута
)

// Сайдбар — QTreeWidget без текста в строках: всё рисует делегат
// (paintSidebarRow), а фон наведения и выбора — QSS (#sidebar::item).
// Группы — категории, как в веб-клиенте (categories.go): свёрнутая
// показывает только непрочитанные и открытый канал; каналы перетаскиваются
// между категориями и внутри них.
func (m *MainWindow) setupSidebar() {
	t := qt.NewQTreeWidget2()
	setName(t.QObject, "sidebar")
	t.SetHeaderHidden(true)
	t.SetRootIsDecorated(false)
	t.SetIndentation(0)
	t.SetUniformRowHeights(true)
	t.SetExpandsOnDoubleClick(false)
	t.SetFocusPolicy(qt.NoFocus)
	t.SetHorizontalScrollBarPolicy(qt.ScrollBarAlwaysOff)
	t.SetMouseTracking(true)
	t.Viewport().SetAttribute(qt.WA_Hover)

	d := qt.NewQStyledItemDelegate2(t.QObject)
	d.OnPaint(func(super func(*qt.QPainter, *qt.QStyleOptionViewItem, *qt.QModelIndex), p *qt.QPainter, opt *qt.QStyleOptionViewItem, idx *qt.QModelIndex) {
		super(p, opt, idx) // фон строки по QSS; текста у элементов нет
		if it := t.ItemFromIndex(idx); it != nil {
			m.paintSidebarRow(p, opt, it)
		}
	})
	d.OnSizeHint(func(super func(*qt.QStyleOptionViewItem, *qt.QModelIndex) *qt.QSize, opt *qt.QStyleOptionViewItem, idx *qt.QModelIndex) *qt.QSize {
		// По дизайну 28; крупный шрифт — выше строка.
		return qt.NewQSize2(opt.Rect().Width(), max(28, qt.NewQFontMetrics(sidebarFont(0, qt.QFont__Normal)).Height()+10))
	})
	t.SetItemDelegate(d.QAbstractItemDelegate)

	t.OnItemClicked(func(it *qt.QTreeWidgetItem, _ int) {
		if it.Data(0, channelIDRole).ToString() == "" && it.Data(0, groupRole).ToString() != "" {
			m.toggleCategory(it)
		}
	})
	// Перетаскивание: каналы (ItemIsDragEnabled) — в группы категорий
	// (ItemIsDropEnabled), не в корень. Qt кладёт в дерево копию строки
	// (CopyAction: исходная не удаляется), сайдбар тут же строится заново
	// из категорий.
	t.SetDragEnabled(true)
	t.SetAcceptDrops(true)
	t.SetDropIndicatorShown(true)
	t.SetDragDropMode(qt.QAbstractItemView__DragDrop)
	t.SetDefaultDropAction(qt.CopyAction)
	t.InvisibleRootItem().SetFlags(qt.ItemIsEnabled)
	t.OnDragMoveEvent(func(super func(*qt.QDragMoveEvent), e *qt.QDragMoveEvent) {
		super(e)
		if _, _, _, _, ok := m.dropTarget(e.Position().ToPoint()); !ok {
			e.Ignore()
		}
	})
	t.OnDropEvent(func(super func(*qt.QDropEvent), e *qt.QDropEvent) {
		team, id, cat, order, ok := m.dropTarget(e.Position().ToPoint())
		if !ok {
			e.Ignore()
			return
		}
		e.SetDropAction(qt.CopyAction)
		super(e)
		mainthread.Start(func() {
			if !m.closed {
				m.moveChannel(team, id, cat, order)
			}
		})
	})
	t.OnCurrentItemChanged(func(cur, _ *qt.QTreeWidgetItem) {
		if cur == nil {
			return
		}
		if id := cur.Data(0, channelIDRole).ToString(); id != "" && id != m.current {
			m.openChannel(id)
		}
	})
	t.SetContextMenuPolicy(qt.CustomContextMenu)
	t.OnCustomContextMenuRequested(m.sidebarMenu)
	m.sidebar = t
}

func (m *MainWindow) fillSidebar() {
	scroll := m.sidebar.VerticalScrollBar().Value()
	collapsed := map[string]bool{} // свёрнутые группы без категорий — по названию
	for i := range m.sidebar.TopLevelItemCount() {
		if g := m.sidebar.TopLevelItem(i); g.Data(0, categoryRole).ToString() == "" && g.Data(0, collapsedRole).ToBool() {
			collapsed[g.Data(0, groupRole).ToString()] = true
		}
	}
	m.sidebar.Clear()
	m.items = map[string]*qt.QTreeWidgetItem{}
	var toSelect *qt.QTreeWidgetItem
	row := func(parent *qt.QTreeWidgetItem, id string, drag bool) *qt.QTreeWidgetItem {
		var it *qt.QTreeWidgetItem
		if parent == nil {
			it = qt.NewQTreeWidgetItem()
			m.sidebar.AddTopLevelItem(it)
		} else {
			it = qt.NewQTreeWidgetItem6(parent)
		}
		it.SetData(0, channelIDRole, qt.NewQVariant11(id))
		flags := qt.ItemIsEnabled | qt.ItemIsSelectable
		if drag {
			flags |= qt.ItemIsDragEnabled
		}
		it.SetFlags(flags)
		m.items[id] = it
		if id == m.current {
			toSelect = it
		}
		return it
	}
	if m.st.CRT() {
		row(nil, threadsID, false)
	}
	if len(m.sched.posts) > 0 {
		row(nil, schedID, false)
	}
	if m.draftCount() > 0 {
		row(nil, draftsID, false)
	}
	multi := len(m.st.Teams()) > 1
	label := ""
	for _, sec := range m.st.Sections() {
		c := sec.Category
		if multi && c != nil && c.Type != model.SidebarCategoryDirectMessages && sec.Team != label {
			label = sec.Team
			l := qt.NewQTreeWidgetItem()
			for _, t := range m.st.Teams() {
				if t.Id == sec.Team {
					l.SetData(0, teamLabelRole, qt.NewQVariant11(t.DisplayName))
				}
			}
			l.SetData(0, teamRole, qt.NewQVariant11(sec.Team))
			l.SetFlags(qt.ItemIsEnabled)
			m.sidebar.AddTopLevelItem(l)
		}
		group := qt.NewQTreeWidgetItem()
		group.SetData(0, groupRole, qt.NewQVariant11(sec.Title))
		group.SetData(0, teamRole, qt.NewQVariant11(sec.Team))
		flags := qt.ItemIsEnabled
		if c != nil {
			group.SetData(0, categoryRole, qt.NewQVariant11(c.Id))
			flags |= qt.ItemIsDropEnabled
		}
		group.SetData(0, collapsedRole, qt.NewQVariant8(sec.Collapsed || c == nil && collapsed[sec.Title]))
		group.SetFlags(flags)
		m.sidebar.AddTopLevelItem(group)
		for _, id := range sec.Channels {
			row(group, id, c != nil)
		}
		group.SetExpanded(true)
	}
	for _, ch := range m.st.DirectChannels() {
		if it := m.items[ch.Id]; it != nil && ch.Type == model.ChannelTypeDirect {
			if cs := status.Current(m.st.User(m.st.DMPeer(ch)), time.Now()); cs != nil {
				it.SetToolTip(0, statusTip(cs))
			}
		}
	}
	if m.current == "" && m.initialChannel != "" {
		toSelect = m.items[m.initialChannel]
		m.initialChannel = ""
	}
	if it := m.items[m.selectAfterLoad]; it != nil {
		toSelect = it
		m.selectAfterLoad = ""
	}
	if m.current == "" && toSelect == nil {
		toSelect = m.items[m.defaultChannel()]
	}
	if toSelect != nil {
		m.sidebar.SetCurrentItem(toSelect)
	}
	m.applyCollapsed()
	m.sidebar.VerticalScrollBar().SetValue(scroll)
	m.updateCounts()
	m.refreshHeader() // канал могли переименовать, поменять шапку
	if path := m.pendingPath; path != "" {
		m.pendingPath = ""
		m.OpenPath(path)
	}
}

// defaultChannel — канал при первом запуске: Town Square первой команды,
// без неё — первый канал команды.
func (m *MainWindow) defaultChannel() string {
	for _, t := range m.st.Teams() {
		chs := m.st.TeamChannels(t.Id)
		for _, ch := range chs {
			if ch.Name == model.DefaultChannelName {
				return ch.Id
			}
		}
		if len(chs) > 0 {
			return chs[0].Id
		}
	}
	return ""
}

// refreshItem перерисовывает строку канала (непрочитанное, упоминания, статус).
func (m *MainWindow) refreshItem(string) {
	if m.st.UnreadSection() && m.unreadGroupStale() {
		m.rebuildSidebarSoon() // канал стал (не)прочитанным — переехал в группу или из неё
	}
	m.applyCollapsed()
	m.sidebar.Viewport().Update()
	m.updateCounts()
}

// applyCollapsed — в свёрнутой группе видны только непрочитанные и
// открытый канал, как в веб-клиенте. Qt раскладывает дерево заново на
// каждый setHidden — поэтому только там, где видимость меняется.
func (m *MainWindow) applyCollapsed() {
	for i := range m.sidebar.TopLevelItemCount() {
		g := m.sidebar.TopLevelItem(i)
		collapsed := g.Data(0, collapsedRole).ToBool()
		for j := range g.ChildCount() {
			it := g.Child(j)
			id := it.Data(0, channelIDRole).ToString()
			unread, mentions := m.st.Unread(id)
			if hide := collapsed && !unread && mentions == 0 && id != m.current; it.IsHidden() != hide {
				it.SetHidden(hide)
			}
		}
	}
}

// updateCounts — счётчики для трея: сообщения мне и все новые.
func (m *MainWindow) updateCounts() {
	if c := (Counts{Mentions: m.st.Mentions(), Unread: m.st.NewMessages()}); c != m.counts {
		m.counts = c
		if m.OnCounts != nil {
			m.OnCounts(c)
		}
	}
}

func (m *MainWindow) paintSidebarRow(p *qt.QPainter, opt *qt.QStyleOptionViewItem, it *qt.QTreeWidgetItem) {
	pal := Pal()
	r := opt.Rect()
	x, y, w, h := r.X()+12, r.Y(), r.Width()-24, r.Height()
	cy := y + h/2
	selected := opt.State()&qt.QStyle__State_Selected != 0
	id := it.Data(0, channelIDRole).ToString()

	text := func(s string, x, width int, color string, weight qt.QFont__Weight) int {
		f := sidebarFont(0, weight)
		fm := qt.NewQFontMetrics(f)
		s = fm.ElidedText(s, qt.ElideRight, width)
		p.SetFont(f)
		p.SetPen(qt.NewQColor6(color))
		p.DrawText7(x, y, width, h, int(qt.AlignVCenter|qt.AlignLeft), s)
		return fm.HorizontalAdvance(s)
	}

	if team := it.Data(0, teamLabelRole).ToString(); team != "" { // команда (при нескольких)
		f := sidebarFont(0, qt.QFont__DemiBold)
		p.SetFont(f)
		p.SetPen(qt.NewQColor6(pal.Tx))
		p.DrawText7(x-2, y, w+2, h, int(qt.AlignBottom|qt.AlignLeft), qt.NewQFontMetrics(f).ElidedText(team, qt.ElideRight, w))
		return
	}
	if id == "" { // группа
		chev := "chevron-down"
		if it.Data(0, collapsedRole).ToBool() {
			chev = "chevron-right"
		}
		p.DrawPixmap9(x-2, cy-6, iconPixmap(chev, pal.Tx2, 12))
		f := sidebarFont(-2, qt.QFont__DemiBold)
		p.SetFont(f)
		p.SetPen(qt.NewQColor6(pal.Tx2))
		p.DrawText7(x+14, y, w-14, h, int(qt.AlignVCenter|qt.AlignLeft), it.Data(0, groupRole).ToString())
		return
	}

	var (
		label    string
		suffix   string // « (вы)» у лички с собой
		unread   bool
		mentions int64
		muted    bool
		iconName string
		avatar   *model.User
		presence string
	)
	switch id {
	case threadsID:
		label, iconName = "Треды", "messages-square"
		unread, mentions = m.st.ThreadsUnread()
	case schedID:
		label, iconName = "Отложенные", "clock"
		suffix = fmt.Sprintf("  %d", len(m.sched.posts))
	case draftsID:
		label, iconName = "Черновики", "pencil"
		suffix = fmt.Sprintf("  %d", m.draftCount())
	default:
		ch := m.st.Channel(id)
		if ch == nil {
			return
		}
		x += 24 // вложенные строки
		unread, mentions = m.st.Unread(id)
		muted = m.st.Muted(id)
		label = m.st.Title(ch)
		switch {
		case ch.DeleteAt > 0:
			iconName = "archive"
		case ch.Type == model.ChannelTypeOpen:
			iconName = "hash"
		case ch.Type == model.ChannelTypePrivate:
			iconName = "lock"
		case ch.Type == model.ChannelTypeGroup:
			iconName = "users"
		case ch.Type == model.ChannelTypeDirect:
			peer := m.st.DMPeer(ch)
			avatar, presence = m.st.User(peer), m.st.Status(peer)
			label = m.st.Name(peer)
			if peer == m.s.Me.Id {
				suffix = " (вы)"
			}
		default:
			iconName = "hash"
		}
	}

	color, weight, iconColor := pal.Tx2, qt.QFont__Normal, pal.Tx2
	switch {
	case selected:
		color, iconColor = pal.SelTx, pal.Acc
		if unread || mentions > 0 {
			weight = qt.QFont__DemiBold
		}
	case muted && mentions == 0:
		color, iconColor = pal.Tx3, pal.Tx3
	case unread || mentions > 0:
		color, weight = pal.Tx, qt.QFont__DemiBold
	}

	if avatar != nil || iconName == "" {
		name := label
		p.DrawPixmap9(x-2, cy-10, avatarPixmap(m.assets, avatar, name, false, 20, m.sidebar.Viewport().Update))
		bg := pal.Side
		if selected {
			bg = pal.AccW
		}
		drawStatusDot(p, float64(x-2+20-7), float64(cy-10+20-7), 7, presence, bg)
	} else {
		p.DrawPixmap9(x, cy-8, iconPixmap(iconName, iconColor, 16))
	}
	tx := x + 24

	// Справа: счётчик упоминаний и колокольчик заглушённого канала.
	right := r.X() + r.Width() - 12
	if mentions > 0 {
		badge := fmt.Sprint(mentions)
		bf := pixelFont(11, qt.QFont__DemiBold)
		bw := max(18, qt.NewQFontMetrics(bf).HorizontalAdvance(badge)+12)
		bg, fg := pal.Acc, pal.OnAcc
		if muted {
			bg, fg = pal.Line2, pal.Tx
		}
		p.Save()
		p.SetRenderHint(qt.QPainter__Antialiasing)
		p.SetPenWithStyle(qt.NoPen)
		p.SetBrush(qt.NewQBrush3(qt.NewQColor6(bg)))
		p.DrawRoundedRect(qt.NewQRectF4(float64(right-bw), float64(cy-9), float64(bw), 18), 9, 9)
		p.SetFont(bf)
		p.SetPen(qt.NewQColor6(fg))
		p.DrawText7(right-bw, cy-9, bw, 18, int(qt.AlignCenter), badge)
		p.Restore()
		right -= bw + 6
	}
	if muted {
		p.DrawPixmap9(right-14, cy-7, iconPixmap("bell-off", pal.Tx3, 14))
		right -= 20
	}
	if m.composer.HasDraft(id) { // недописанное сообщение, как в веб-клиенте
		p.DrawPixmap9(right-14, cy-7, iconPixmap("pencil", pal.Tx3, 14))
		right -= 20
	}
	used := text(label, tx, right-tx, color, weight)
	if suffix != "" {
		used += text(suffix, tx+used, right-tx-used, pal.Tx2, qt.QFont__Normal)
	}
	// Свой статус собеседника — эмодзи после имени (подсказка — у строки).
	if avatar != nil && tx+used+20 <= right {
		if cs := status.Current(avatar, time.Now()); cs != nil {
			p.DrawPixmap9(tx+used+6, cy-8, m.emojiImage(cs.Emoji, 16, m.sidebar.Viewport().Update))
		}
	}
}

// sidebarOrder — что можно открыть в сайдбаре, сверху вниз, как видно:
// «Треды», «Отложенные», каналы и лички (без скрытых в свёрнутых категориях).
func (m *MainWindow) sidebarOrder() []string {
	var out []string
	add := func(it *qt.QTreeWidgetItem) {
		if id := it.Data(0, channelIDRole).ToString(); id != "" && !it.IsHidden() {
			out = append(out, id)
		}
	}
	for i := range m.sidebar.TopLevelItemCount() {
		g := m.sidebar.TopLevelItem(i)
		add(g)
		for j := range g.ChildCount() {
			add(g.Child(j))
		}
	}
	return out
}

// stepChannel — Alt+↑/↓ (delta −1/+1): соседний канал в сайдбаре; unread —
// ближайший с непрочитанным (Alt+Shift+↑/↓). По кругу, как в веб-клиенте.
func (m *MainWindow) stepChannel(delta int, unread bool) {
	order := m.sidebarOrder()
	n := len(order)
	if n == 0 {
		return
	}
	i := slices.Index(order, m.current)
	if i < 0 && delta < 0 {
		i = n // ничего не открыто: вверх — с последнего
	}
	for step := 1; step <= n; step++ {
		id := order[((i+delta*step)%n+n)%n]
		if id == m.current || unread && !m.rowUnread(id) {
			continue
		}
		m.selectChannel(id)
		return
	}
}

// rowUnread — у строки сайдбара есть непрочитанное или упоминания.
func (m *MainWindow) rowUnread(id string) bool {
	if id == threadsID {
		unread, mentions := m.st.ThreadsUnread()
		return unread || mentions > 0
	}
	unread, mentions := m.st.Unread(id)
	return unread || mentions > 0
}

// unreadGroupStale — группа «Непрочитанные» в дереве не та, что сейчас
// (состав или порядок): сайдбар надо перестроить.
func (m *MainWindow) unreadGroupStale() bool {
	var want, have []string
	if secs := m.st.Sections(); len(secs) > 0 && secs[0].Category == nil && secs[0].Title == store.UnreadTitle {
		want = secs[0].Channels
	}
	for i := range m.sidebar.TopLevelItemCount() {
		g := m.sidebar.TopLevelItem(i)
		if g.Data(0, groupRole).ToString() == store.UnreadTitle && g.Data(0, categoryRole).ToString() == "" {
			for j := range g.ChildCount() {
				have = append(have, g.Child(j).Data(0, channelIDRole).ToString())
			}
			break
		}
	}
	return !slices.Equal(want, have)
}

// rebuildSidebarSoon — перестроить сайдбар после текущего события: зовут и
// из выбора строки (открыли канал — он прочитан), а удалять строки дерева
// внутри его сигнала нельзя. Повторы склеиваются.
func (m *MainWindow) rebuildSidebarSoon() {
	if m.rebuildPending {
		return
	}
	m.rebuildPending = true
	mainthread.Start(func() {
		m.rebuildPending = false
		if !m.closed {
			m.fillSidebar()
		}
	})
}
