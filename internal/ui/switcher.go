package ui

import (
	"cmp"
	"context"
	"slices"
	"strings"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/complete"
)

// Быстрый переход (Ctrl+K, как в веб-клиенте): окно с поиском по своим
// каналам и личкам, по людям (личка с ними — откроется или создастся) и, с
// сервера, по открытым каналам, где нас нет (вход). Пусто — недавние.
// ↑/↓ — выбрать, Enter или двойной щелчок — перейти, Esc — закрыть.

const switchLimit = 30

type switchKind int

const (
	switchChannel switchKind = iota // свой канал или личка
	switchUser                      // человек без лички — личка с ним
	switchJoin                      // открытый канал, где нас нет, — войти
)

type switchItem struct {
	kind       switchKind
	id         string         // канал (switchChannel, switchJoin) или пользователь
	channel    *model.Channel // у switchJoin
	title, sub string
	fields     []string // по чему искать: первое — главное
	unread     bool
	viewed     int64 // когда канал был прочитан: недавние — выше
	icon       *qt.QPixmap
}

// switchSources — из чего выбирать: свои каналы и лички, люди, и что
// добавил сервер (люди, каналы, где нас нет).
func (m *MainWindow) switchSources(remoteUsers []*model.User, remoteChannels []*model.Channel) []switchItem {
	pal := Pal()
	var out []switchItem
	multiTeam := len(m.st.Teams()) > 1
	withDM := map[string]bool{}
	for _, t := range m.st.Teams() {
		for _, ch := range m.st.TeamChannels(t.Id) {
			unread, _ := m.st.Unread(ch.Id)
			iconName := "hash"
			if ch.Type == model.ChannelTypePrivate {
				iconName = "lock"
			}
			sub := "~" + ch.Name
			if multiTeam {
				sub += " · " + t.DisplayName
			}
			out = append(out, switchItem{kind: switchChannel, id: ch.Id, title: ch.DisplayName, sub: sub,
				fields: []string{ch.DisplayName, ch.Name, t.DisplayName}, unread: unread, viewed: m.st.LastViewed(ch.Id),
				icon: iconPixmap(iconName, pal.Tx2, 20)})
		}
	}
	for _, ch := range m.st.DirectChannels() {
		unread, _ := m.st.Unread(ch.Id)
		it := switchItem{kind: switchChannel, id: ch.Id, title: m.st.Title(ch), unread: unread, viewed: m.st.LastViewed(ch.Id)}
		if ch.Type == model.ChannelTypeDirect {
			peer := m.st.DMPeer(ch)
			withDM[peer] = true
			u := m.st.User(peer)
			it.title = m.st.Username(peer)
			it.fields = []string{it.title}
			if u != nil {
				it.sub = displayName(u)
				it.fields = append(it.fields, u.FirstName, u.LastName, u.Nickname, displayName(u))
			}
			if it.sub == it.title {
				it.sub = ""
			}
			if peer == m.s.Me.Id {
				it.sub += " (вы)"
			}
			it.icon = avatarPixmap(m.assets, u, it.title, u != nil && u.IsBot, 20, nil)
		} else {
			it.fields = []string{it.title}
			it.icon = iconPixmap("users", pal.Tx2, 20)
		}
		out = append(out, it)
	}
	seen := map[string]bool{}
	for _, u := range append(m.st.KnownUsers(), remoteUsers...) {
		if u == nil || u.DeleteAt > 0 || withDM[u.Id] || seen[u.Id] {
			continue
		}
		seen[u.Id] = true
		sub := "написать"
		if dn := displayName(u); dn != u.Username {
			sub = dn + " · написать"
		}
		out = append(out, switchItem{kind: switchUser, id: u.Id, title: u.Username, sub: sub,
			fields: []string{u.Username, u.FirstName, u.LastName, u.Nickname, displayName(u)},
			icon:   avatarPixmap(m.assets, u, u.Username, u.IsBot, 20, nil)})
	}
	for _, ch := range remoteChannels {
		if ch == nil || m.st.Channel(ch.Id) != nil || ch.Type != model.ChannelTypeOpen || seen[ch.Id] {
			continue
		}
		seen[ch.Id] = true
		out = append(out, switchItem{kind: switchJoin, id: ch.Id, channel: ch, title: ch.DisplayName, sub: "~" + ch.Name + " · войти",
			fields: []string{ch.DisplayName, ch.Name}, icon: iconPixmap("hash", pal.Tx3, 20)})
	}
	return out
}

// rankSwitch — что показать под запросом q: пусто — недавние свои каналы и
// лички (кроме открытого); иначе подходящие — лучше совпавшие, свои раньше
// людей и каналов, где нас нет, непрочитанные и недавние выше.
func rankSwitch(items []switchItem, q, current string) []switchItem {
	q = strings.TrimSpace(q)
	type scored struct {
		switchItem
		score int
	}
	var out []scored
	for _, it := range items {
		switch {
		case q == "":
			if it.kind == switchChannel && it.id != current {
				out = append(out, scored{it, 0})
			}
		default:
			if s := complete.Score(q, it.fields...); s > 0 {
				out = append(out, scored{it, s})
			}
		}
	}
	slices.SortStableFunc(out, func(a, b scored) int {
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		if c := cmp.Compare(a.kind, b.kind); c != 0 {
			return c
		}
		if a.unread != b.unread {
			if a.unread {
				return -1
			}
			return 1
		}
		if c := cmp.Compare(b.viewed, a.viewed); c != 0 {
			return c
		}
		return cmp.Compare(strings.ToLower(a.title), strings.ToLower(b.title))
	})
	res := make([]switchItem, 0, min(len(out), switchLimit))
	for _, s := range out[:min(len(out), switchLimit)] {
		res = append(res, s.switchItem)
	}
	return res
}

// showSwitcher — Ctrl+K: окно быстрого перехода.
func (m *MainWindow) showSwitcher() {
	d := newDialog(m.w, "Перейти к каналу")
	m.switcherDialog = d
	field := qt.NewQLineEdit2()
	field.SetPlaceholderText("Канал, личка или человек")
	field.SetClearButtonEnabled(true)
	list := qt.NewQListWidget2()
	list.SetIconSize(qt.NewQSize2(20, 20))
	list.SetMinimumHeight(320)
	hint := qt.NewQLabel3("↑↓ — выбрать, Enter — перейти, Esc — закрыть")
	hint.SetProperty("role", qt.NewQVariant11("hint"))
	d.lay.AddWidget(field.QWidget)
	d.lay.AddWidget(list.QWidget)
	d.lay.AddWidget(hint.QWidget)

	var shown []switchItem
	var remoteUsers []*model.User
	var remoteChannels []*model.Channel
	fill := func() {
		shown = rankSwitch(m.switchSources(remoteUsers, remoteChannels), field.Text(), m.current)
		list.Clear()
		for _, it := range shown {
			text := it.title
			if it.sub != "" {
				text += "   " + it.sub
			}
			row := qt.NewQListWidgetItem7(text, list)
			row.SetIcon(qt.NewQIcon2(it.icon))
			if it.unread {
				f := row.Font()
				f.SetBold(true)
				row.SetFont(f)
			}
		}
		if len(shown) > 0 {
			list.SetCurrentRow(0)
		}
	}
	open := func() {
		i := list.CurrentRow()
		if i < 0 || i >= len(shown) {
			return
		}
		it := shown[i]
		d.w.Close()
		switch it.kind {
		case switchChannel:
			m.selectChannel(it.id)
		case switchUser:
			m.openDM(it.id)
		case switchJoin:
			m.joinChannel(it.channel, func(err error) {
				if err != nil {
					m.fail("join channel", err)
				}
			})
		}
	}
	// С сервера — через 150 мс после паузы в наборе; ответ на прежний текст — прочь.
	gen := 0
	timer := qt.NewQTimer2(d.w.QObject)
	timer.SetSingleShot(true)
	timer.SetInterval(150)
	timer.OnTimeout(func() {
		q := strings.TrimSpace(field.Text())
		if q == "" {
			return
		}
		gen++
		mine := gen
		teams := m.st.Teams()
		go func() {
			ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
			defer cancel()
			var users []*model.User
			var chans []*model.Channel
			for i, t := range teams {
				if i == 0 { // люди — общие на сервере: одной команды хватит
					if ua, _, err := m.s.Client.AutocompleteUsersInTeam(ctx, t.Id, q, switchLimit, ""); err == nil && ua != nil {
						users = append(users, ua.Users...)
					}
				}
				if cs, _, err := m.s.Client.AutocompleteChannelsForTeam(ctx, t.Id, q); err == nil {
					chans = append(chans, cs...)
				}
			}
			m.ui(func() {
				if d.closed || mine != gen {
					return
				}
				m.st.AddUsers(users)
				remoteUsers, remoteChannels = users, chans
				fill()
			})
		}()
	})
	field.OnTextChanged(func(string) {
		remoteUsers, remoteChannels = nil, nil
		fill()
		timer.Start2()
	})
	field.OnKeyPressEvent(func(super func(*qt.QKeyEvent), e *qt.QKeyEvent) {
		switch qt.Key(e.Key()) {
		case qt.Key_Down:
			list.SetCurrentRow(min(list.CurrentRow()+1, list.Count()-1))
		case qt.Key_Up:
			list.SetCurrentRow(max(list.CurrentRow()-1, 0))
		case qt.Key_Return, qt.Key_Enter:
			open()
		default:
			super(e)
		}
	})
	list.OnItemActivated(func(*qt.QListWidgetItem) { open() })
	fill()
	d.w.Resize(520, 0)
	d.w.Show()
	field.SetFocus()
}
