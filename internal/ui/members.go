package ui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/complete"
)

// Участники канала: кнопка с их числом в шапке канала и «Участники» в меню
// канала открывают список в правой колонке (на месте треда и поиска):
// фильтр, «Добавить…», администраторы канала первыми. Щелчок по строке —
// карточка, правый — «Написать», «Удалить из канала». Добавить — окно
// выбора людей (people.go): кто в команде, но не в канале, — с сервера.

const (
	memberIDRole = int(qt.UserRole) + 6
	membersPage  = 200
	membersPages = 25 // до 5000 участников
)

type membersPanel struct {
	w      *qt.QWidget
	head   *header
	filter *qt.QLineEdit
	add    *qt.QPushButton
	info   *qt.QLabel
	list   *qt.QListWidget
	reload *qt.QTimer // события об участниках идут пачкой — перечитываем раз

	channel string // чьи участники показаны
	users   []*model.User
	admins  map[string]bool
	gen     int

	unsubscribe func()
}

func (m *MainWindow) newMembersPanel() *membersPanel {
	mp := &membersPanel{w: qt.NewQWidget2(), filter: qt.NewQLineEdit2(), info: qt.NewQLabel2(), list: qt.NewQListWidget2()}
	mp.head = newHeader("threadHeader", true, nil)
	mp.head.close.OnClicked(func() { m.showRight(nil) })
	mp.filter.SetPlaceholderText("Найти участника")
	mp.filter.SetClearButtonEnabled(true)
	mp.filter.OnTextChanged(func(string) { mp.fill() })
	mp.add = qt.NewQPushButton3("Добавить…")
	mp.add.OnClicked(func() { m.showAddMembers(mp.channel) })
	mp.info.SetProperty("role", qt.NewQVariant11("hint"))

	l := mp.list
	setName(l.QObject, "members")
	l.SetFrameShape(qt.QFrame__NoFrame)
	l.SetUniformItemSizes(true)
	l.SetHorizontalScrollBarPolicy(qt.ScrollBarAlwaysOff)
	l.SetMouseTracking(true)
	d := qt.NewQStyledItemDelegate2(l.QObject)
	d.OnPaint(func(super func(*qt.QPainter, *qt.QStyleOptionViewItem, *qt.QModelIndex), p *qt.QPainter, opt *qt.QStyleOptionViewItem, idx *qt.QModelIndex) {
		noFocusFrame(opt)
		super(p, opt, idx)
		if it := l.ItemFromIndex(idx); it != nil {
			if u := m.st.User(it.Data(memberIDRole).ToString()); u != nil {
				m.paintMemberRow(p, opt, u, mp.admins[u.Id])
			}
		}
	})
	d.OnSizeHint(func(super func(*qt.QStyleOptionViewItem, *qt.QModelIndex) *qt.QSize, opt *qt.QStyleOptionViewItem, idx *qt.QModelIndex) *qt.QSize {
		return qt.NewQSize2(opt.Rect().Width(), 44)
	})
	l.SetItemDelegate(d.QAbstractItemDelegate)
	l.OnItemClicked(func(it *qt.QListWidgetItem) {
		if u := m.st.User(it.Data(memberIDRole).ToString()); u != nil {
			m.openUserCard(u, qt.QCursor_Pos())
		}
	})
	l.SetContextMenuPolicy(qt.CustomContextMenu)
	l.OnCustomContextMenuRequested(func(pos *qt.QPoint) {
		it := l.ItemAt(pos)
		if it == nil {
			return
		}
		items := m.memberItems(mp.channel, it.Data(memberIDRole).ToString())
		if len(items) == 0 {
			return
		}
		menu := qt.NewQMenu(l.QWidget)
		defer menu.DeleteLater()
		fillMenu(menu, items)
		menu.ExecWithPos(l.Viewport().MapToGlobalWithQPoint(pos))
	})
	mp.reload = qt.NewQTimer2(mp.w.QObject)
	mp.reload.SetSingleShot(true)
	mp.reload.SetInterval(300)
	mp.reload.OnTimeout(func() {
		if m.membersShown() {
			m.loadMembers(mp.channel)
		}
	})

	row := qt.NewQHBoxLayout2()
	row.SetSpacing(8)
	row.AddWidget2(mp.filter.QWidget, 1)
	row.AddWidget(mp.add.QWidget)
	top := qt.NewQVBoxLayout2()
	top.SetContentsMargins(16, 8, 16, 6)
	top.SetSpacing(4)
	top.AddLayout(row.QLayout)
	top.AddWidget(mp.info.QWidget)
	lay := qt.NewQVBoxLayout(mp.w)
	lay.SetContentsMargins(0, 0, 0, 0)
	lay.SetSpacing(0)
	lay.AddWidget(mp.head.f.QWidget)
	lay.AddLayout(top.QLayout)
	lay.AddWidget2(l.QWidget, 1)
	mp.w.SetVisible(false)
	mp.unsubscribe = OnThemeChanged(mp.restyle)
	mp.restyle()
	return mp
}

func (mp *membersPanel) restyle() {
	mp.head.close.SetIcon(icon("x", Pal().Tx2, 18))
	mp.head.icon.SetPixmap(iconPixmap("users", Pal().Tx2, 16))
	mp.list.Viewport().Update()
}

// paintMemberRow — аватар со статусом, имя пользователя, имя и фамилия,
// справа — «администратор» у администраторов канала.
func (m *MainWindow) paintMemberRow(p *qt.QPainter, opt *qt.QStyleOptionViewItem, u *model.User, admin bool) {
	pal := Pal()
	r := opt.Rect()
	x, y, h := r.X()+16, r.Y(), r.Height()
	cy := y + h/2
	ring := pal.Surf
	if opt.State()&qt.QStyle__State_Selected != 0 {
		ring = pal.AccW
	}
	p.DrawPixmap9(x, cy-14, avatarPixmap(m.assets, u, u.Username, u.IsBot, 28, m.members.list.Viewport().Update))
	drawStatusDot(p, float64(x+28-9), float64(cy-14+28-9), 9, m.st.Status(u.Id), ring)
	tx := x + 28 + 10
	right := r.X() + r.Width() - 16
	if admin {
		f := sidebarFont(-2, qt.QFont__Normal)
		fm := qt.NewQFontMetrics(f)
		label := "администратор"
		w := fm.HorizontalAdvance(label)
		p.SetFont(f)
		p.SetPen(qt.NewQColor6(pal.Tx3))
		p.DrawText7(right-w, y, w, h, int(qt.AlignVCenter|qt.AlignLeft), label)
		right -= w + 8
	}
	name := u.Username
	if u.Id == m.s.Me.Id {
		name += " (вы)"
	}
	f := sidebarFont(0, qt.QFont__DemiBold)
	fm := qt.NewQFontMetrics(f)
	name = fm.ElidedText(name, qt.ElideRight, max(right-tx, 0))
	p.SetFont(f)
	p.SetPen(qt.NewQColor6(pal.Tx))
	p.DrawText7(tx, y, right-tx, h, int(qt.AlignVCenter|qt.AlignLeft), name)
	tx += fm.HorizontalAdvance(name) + 8
	if dn := displayName(u); dn != u.Username && tx < right {
		f := sidebarFont(0, qt.QFont__Normal)
		p.SetFont(f)
		p.SetPen(qt.NewQColor6(pal.Tx2))
		dn = qt.NewQFontMetrics(f).ElidedText(dn, qt.ElideRight, right-tx)
		p.DrawText7(tx, y, right-tx, h, int(qt.AlignVCenter|qt.AlignLeft), dn)
	}
}

// fill — участники под фильтром.
func (mp *membersPanel) fill() {
	q := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(mp.filter.Text()), "@"))
	mp.list.Clear()
	n := 0
	for _, u := range mp.users {
		if q != "" && complete.Score(q, userFields(u)...) == 0 {
			continue
		}
		it := qt.NewQListWidgetItem5(mp.list)
		it.SetData(memberIDRole, qt.NewQVariant11(u.Id))
		n++
	}
	switch {
	case mp.users == nil:
	case q != "":
		mp.info.SetText(fmt.Sprintf("Найдено: %d из %d", n, len(mp.users)))
	default:
		mp.info.SetText(pluralMembers(int64(len(mp.users))))
	}
}

func pluralMembers(n int64) string {
	return plural(n, "участник", "участника", "участников")
}

// membersShown — открыт ли список участников (в окне, свёрнутом в трей, тоже).
func (m *MainWindow) membersShown() bool { return !m.members.w.IsHidden() }

// toggleMembers — кнопка в шапке: участники открытого канала или закрыть.
func (m *MainWindow) toggleMembers() {
	if m.membersShown() && m.members.channel == m.current {
		m.showRight(nil)
		return
	}
	m.showMembers(m.current)
}

// showMembers — участники канала в правой колонке.
func (m *MainWindow) showMembers(channelID string) {
	mp := m.members
	ch := m.st.Channel(channelID)
	if ch == nil || m.membersShown() && mp.channel == channelID {
		return
	}
	if m.thread != nil && !m.threadDetached() { // отстёгнутый тред правой колонке не мешает
		m.closeThread()
	}
	mp.channel = channelID
	mp.head.set(iconPixmap("users", Pal().Tx2, 16), "Участники", channelPrefix(ch)+m.st.Title(ch))
	mp.add.SetVisible(!ch.IsGroupOrDirect())
	mp.users, mp.admins = nil, nil
	mp.filter.Clear()
	mp.fill()
	mp.info.SetText("Загружаем…")
	m.showRight(mp.w)
	m.loadMembers(channelID)
}

// loadMembers — участники канала с сервера: профили, кто администратор,
// статусы; по порядку — администраторы, потом по имени.
func (m *MainWindow) loadMembers(channelID string) {
	mp := m.members
	mp.gen++
	gen := mp.gen
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		var users []*model.User
		var members model.ChannelMembers
		var err error
		for page := 0; page < membersPages; page++ {
			batch, _, e := m.s.Client.GetUsersInChannel(ctx, channelID, page, membersPage, "")
			if err = e; e != nil {
				break
			}
			users = append(users, batch...)
			if len(batch) < membersPage {
				break
			}
		}
		for page := 0; err == nil && page < membersPages; page++ {
			batch, _, e := m.s.Client.GetChannelMembers(ctx, channelID, page, membersPage, "")
			if err = e; e != nil {
				break
			}
			members = append(members, batch...)
			if len(batch) < membersPage {
				break
			}
		}
		var statuses []*model.Status
		if err == nil && len(users) > 0 {
			ids := make([]string, len(users))
			for i, u := range users {
				ids[i] = u.Id
			}
			var e error
			if statuses, _, e = m.s.Client.GetUsersStatusesByIds(ctx, ids); e != nil {
				m.log.Warn("load member statuses", "channel", channelID, "err", e)
			}
		}
		m.ui(func() {
			if mp.gen != gen || mp.channel != channelID {
				return
			}
			if err != nil {
				m.log.Warn("load channel members", "channel", channelID, "err", err)
				mp.info.SetText("Не загрузилось: " + commandError(err))
				return
			}
			m.st.AddUsers(users)
			for _, s := range statuses {
				m.st.SetStatus(s.UserId, s.Status)
			}
			admins := map[string]bool{}
			for _, cm := range members {
				if cm.SchemeAdmin {
					admins[cm.UserId] = true
				}
			}
			slices.SortFunc(users, func(a, b *model.User) int {
				if admins[a.Id] != admins[b.Id] {
					if admins[a.Id] {
						return -1
					}
					return 1
				}
				return cmp.Compare(strings.ToLower(a.Username), strings.ToLower(b.Username))
			})
			mp.users, mp.admins = users, admins
			if len(users) < membersPage*membersPages {
				m.memberCounts[channelID] = int64(len(users))
				m.updateMembersButton()
			}
			mp.fill()
		})
	}()
}

// memberItems — меню участника userID в списке канала.
func (m *MainWindow) memberItems(channelID, userID string) []menuItem {
	u, ch := m.st.User(userID), m.st.Channel(channelID)
	if u == nil || ch == nil {
		return nil
	}
	out := []menuItem{
		{title: "Профиль", do: func() { m.openUserCard(u, qt.QCursor_Pos()) }},
		{title: "Написать", do: func() { m.openDM(userID) }},
	}
	// Из главного канала команды сервер не выпускает никого, из личек — тоже.
	if ch.IsGroupOrDirect() || ch.Name == model.DefaultChannelName {
		return out
	}
	if userID == m.s.Me.Id {
		return append(out, menuItem{}, menuItem{title: "Покинуть канал…", do: func() { m.confirmLeave(channelID) }})
	}
	return append(out, menuItem{}, menuItem{title: "Удалить из канала", do: func() { m.removeMember(channelID, userID) }})
}

// removeMember — удалить участника из канала (как «Remove from channel» веб-клиента, без вопроса).
func (m *MainWindow) removeMember(channelID, userID string) {
	name := m.st.Username(userID)
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		_, err := m.s.Client.RemoveUserFromChannel(ctx, channelID, userID)
		m.ui(func() {
			if err != nil {
				m.fail("remove member", fmt.Errorf("remove @%s from channel %s: %w", name, channelID, err))
				return
			}
			m.message("@" + name + " удалён из канала")
			m.membersChanged(channelID)
		})
	}()
}

// showAddMembers — «Добавить участников…»: выбрать людей команды, кого в
// канале нет, и добавить разом. Возвращает окно выбора (тесты).
func (m *MainWindow) showAddMembers(channelID string) *peoplePicker {
	ch := m.st.Channel(channelID)
	if ch == nil || ch.IsGroupOrDirect() {
		return nil
	}
	d := newDialog(m.w, "Добавить в "+channelPrefix(ch)+m.st.Title(ch))
	m.channelDialog = d
	d.w.SetMinimumWidth(460)
	team := ch.TeamId
	pp := m.newPeoplePicker(d, 0, nil, func(ctx context.Context, q string) ([]*model.User, error) {
		if q == "" {
			users, _, err := m.s.Client.GetUsersNotInChannel(ctx, team, channelID, 0, peopleLimit, "")
			return users, err
		}
		users, _, err := m.s.Client.SearchUsers(ctx, &model.UserSearch{Term: q, TeamId: team, NotInChannelId: channelID, Limit: peopleLimit})
		return users, err
	})
	d.bottom.AddStretch()
	cancel := qt.NewQPushButton3("Отмена")
	cancel.OnClicked(func() { d.w.Close() })
	ok := qt.NewQPushButton3("Добавить")
	ok.SetProperty("primary", qt.NewQVariant11("true"))
	ok.SetEnabled(false)
	pp.OnChange = func() { ok.SetEnabled(len(pp.chosen) > 0) }
	pp.OnSubmit = func() {
		ids := pp.ids()
		if len(ids) == 0 {
			return
		}
		ok.SetEnabled(false)
		pp.hint.SetText("Добавляем…")
		go func() {
			ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
			defer cancel()
			_, _, err := m.s.Client.AddChannelMembers(ctx, channelID, "", ids)
			m.ui(func() {
				if err != nil {
					m.log.Warn("add channel members", "channel", channelID, "users", ids, "err", err)
					if !d.closed {
						ok.SetEnabled(true)
						pp.hint.SetText("Не добавлены: " + commandError(err))
					}
					return
				}
				if !d.closed {
					d.w.Close()
				}
				m.message("Добавлено в " + channelPrefix(ch) + m.st.Title(ch) + ": " + strconv.Itoa(len(ids)))
				m.membersChanged(channelID)
			})
		}()
	}
	ok.OnClicked(pp.OnSubmit)
	d.bottom.AddWidget(cancel.QWidget)
	d.bottom.AddWidget(ok.QWidget)
	d.w.Show()
	pp.field.SetFocus()
	return pp
}

// membersChanged — в канале кто-то пришёл или ушёл: число в шапке и
// открытый список — заново.
func (m *MainWindow) membersChanged(channelID string) {
	if channelID == "" {
		return
	}
	delete(m.memberCounts, channelID)
	if channelID == m.current {
		m.updateMembersButton()
	}
	if m.membersShown() && m.members.channel == channelID {
		m.members.reload.Start2()
	}
}

// updateMembersButton — кнопка участников в шапке открытого канала: их
// число (у лички кнопки нет); не знаем — спросим сервер.
func (m *MainWindow) updateMembersButton() {
	b := m.membersBtn
	b.SetIcon(icon("users", Pal().Tx2, 18))
	ch := m.st.Channel(m.current)
	if ch == nil || ch.Type == model.ChannelTypeDirect {
		b.SetVisible(false)
		return
	}
	b.SetVisible(true)
	n, ok := m.memberCounts[ch.Id]
	switch {
	case !ok:
		b.SetText("")
		b.SetToolTip("Участники канала")
		m.loadMemberCount(ch.Id)
	case n >= 0:
		b.SetText(strconv.FormatInt(n, 10))
		b.SetToolTip("Участники канала: " + pluralMembers(n))
	}
}

// loadMemberCount — число участников канала с сервера; -1 — запрос в пути.
func (m *MainWindow) loadMemberCount(channelID string) {
	m.memberCounts[channelID] = -1
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		stats, _, err := m.s.Client.GetChannelStats(ctx, channelID, "", true)
		m.ui(func() {
			if err != nil {
				m.log.Warn("load channel stats", "channel", channelID, "err", err)
				delete(m.memberCounts, channelID)
				return
			}
			m.memberCounts[channelID] = stats.MemberCount
			if channelID == m.current {
				m.updateMembersButton()
			}
		})
	}()
}
