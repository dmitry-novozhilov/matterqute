package ui

import (
	"fmt"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/status"
)

// Роли данных строк сайдбара.
const (
	channelIDRole = int(qt.UserRole) + 1 // id канала или threadsID; у групп пусто
	groupRole     = int(qt.UserRole) + 4 // подпись группы (команда, «Личные сообщения»)
)

// Сайдбар — QTreeWidget без текста в строках: всё рисует делегат
// (paintSidebarRow), а фон наведения и выбора — QSS (#sidebar::item).
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
		if it.Data(0, channelIDRole).ToString() == "" {
			it.SetExpanded(!it.IsExpanded())
		}
	})
	t.OnCurrentItemChanged(func(cur, _ *qt.QTreeWidgetItem) {
		if cur == nil {
			return
		}
		if id := cur.Data(0, channelIDRole).ToString(); id != "" && id != m.current {
			m.openChannel(id)
		}
	})
	m.sidebar = t
}

func (m *MainWindow) fillSidebar() {
	m.sidebar.Clear()
	m.items = map[string]*qt.QTreeWidgetItem{}
	var toSelect *qt.QTreeWidgetItem
	row := func(parent *qt.QTreeWidgetItem, id string) *qt.QTreeWidgetItem {
		var it *qt.QTreeWidgetItem
		if parent == nil {
			it = qt.NewQTreeWidgetItem()
			m.sidebar.AddTopLevelItem(it)
		} else {
			it = qt.NewQTreeWidgetItem6(parent)
		}
		it.SetData(0, channelIDRole, qt.NewQVariant11(id))
		m.items[id] = it
		if id == m.current {
			toSelect = it
		}
		return it
	}
	if m.st.CRT() {
		row(nil, threadsID)
	}
	addGroup := func(title string, chs []*model.Channel) {
		if len(chs) == 0 {
			return
		}
		group := qt.NewQTreeWidgetItem()
		group.SetData(0, groupRole, qt.NewQVariant11(title))
		group.SetFlags(qt.ItemIsEnabled)
		m.sidebar.AddTopLevelItem(group)
		for _, ch := range chs {
			row(group, ch.Id)
		}
		group.SetExpanded(true)
	}
	for _, t := range m.st.Teams() {
		addGroup(t.DisplayName, m.st.TeamChannels(t.Id))
	}
	addGroup("Личные сообщения", m.st.DirectChannels())
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
	m.updateMentions()
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
	m.sidebar.Viewport().Update()
	m.updateMentions()
}

// updateMentions — счётчик для трея: упоминания и непрочитанные лички.
func (m *MainWindow) updateMentions() {
	if n := m.st.Mentions(); n != m.mentions {
		m.mentions = n
		if m.OnMentions != nil {
			m.OnMentions(n)
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

	if id == "" { // группа
		chev := "chevron-right"
		if it.IsExpanded() {
			chev = "chevron-down"
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
	if id == threadsID {
		label, iconName = "Треды", "messages-square"
		unread, mentions = m.st.ThreadsUnread()
	} else {
		ch := m.st.Channel(id)
		if ch == nil {
			return
		}
		x += 24 // вложенные строки
		unread, mentions = m.st.Unread(id)
		muted = m.st.Muted(id)
		label = m.st.Title(ch)
		switch ch.Type {
		case model.ChannelTypeOpen:
			iconName = "hash"
		case model.ChannelTypePrivate:
			iconName = "lock"
		case model.ChannelTypeGroup:
			iconName = "users"
		case model.ChannelTypeDirect:
			peer := m.st.DMPeer(ch)
			avatar, presence = m.st.User(peer), m.st.Status(peer)
			label = m.st.Username(peer)
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
