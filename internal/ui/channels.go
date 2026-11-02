package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/store"
)

// Каналы: обзор открытых и вход, создание, выход, выключить и включить
// уведомления (заглушённый канал непрочитанным считается только при
// упоминании — mark_unread=mention, как в веб-клиенте). Действия — в
// контекстном меню сайдбара (на канале и на команде) и в меню «Файл»;
// сайдбар обновляется по ответу и по событиям сервера.

// menuItem — пункт контекстного меню; title "" — разделитель.
type menuItem struct {
	title string
	do    func()
}

// sidebarMenu — правый клик по сайдбару.
func (m *MainWindow) sidebarMenu(pos *qt.QPoint) {
	it := m.sidebar.ItemAt(pos)
	if it == nil {
		return
	}
	items := m.sidebarItems(it.Data(0, teamRole).ToString(), it.Data(0, channelIDRole).ToString())
	if len(items) == 0 {
		return
	}
	menu := qt.NewQMenu(m.sidebar.QWidget)
	defer menu.DeleteLater()
	for _, x := range items {
		if x.title == "" {
			menu.AddSeparator()
			continue
		}
		menu.QWidget.AddActionWithText(x.title).OnTriggered(x.do)
	}
	menu.ExecWithPos(m.sidebar.Viewport().MapToGlobalWithQPoint(pos))
}

// sidebarItems — что можно сделать с командой team или каналом channelID.
func (m *MainWindow) sidebarItems(team, channelID string) []menuItem {
	var out []menuItem
	if team != "" {
		out = append(out, menuItem{"Обзор каналов…", func() { m.showBrowseChannels(team) }},
			menuItem{"Создать канал…", func() { m.showCreateChannel(team) }})
	}
	ch := m.st.Channel(channelID)
	if ch == nil {
		return out
	}
	id := ch.Id
	if m.st.Muted(id) {
		out = append(out, menuItem{"Включить уведомления", func() { m.setMuted(id, false) }})
	} else {
		out = append(out, menuItem{"Выключить уведомления", func() { m.setMuted(id, true) }})
	}
	if link := m.channelLink(ch); link != "" {
		out = append(out, menuItem{"Копировать ссылку на канал", func() {
			qt.QGuiApplication_Clipboard().SetText(link)
			m.message("Ссылка на канал скопирована")
		}})
	}
	// Из главного канала команды сервер не выпускает — как веб-клиент, пункта нет.
	if !ch.IsGroupOrDirect() && ch.Name != model.DefaultChannelName {
		out = append(out, menuItem{}, menuItem{"Покинуть канал…", func() { m.confirmLeave(id) }})
	}
	return out
}

// channelLink — ссылка на канал, как у веб-клиента; у личек — пусто.
func (m *MainWindow) channelLink(ch *model.Channel) string {
	for _, t := range m.st.Teams() {
		if t.Id == ch.TeamId {
			return strings.TrimSuffix(m.s.ServerURL, "/") + "/" + t.Name + "/channels/" + ch.Name
		}
	}
	return ""
}

// setMuted — выключить (muted) или включить уведомления канала.
func (m *MainWindow) setMuted(channelID string, muted bool) {
	mark := model.ChannelMarkUnreadAll
	if muted {
		mark = model.ChannelMarkUnreadMention
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		_, err := m.s.Client.UpdateChannelNotifyProps(ctx, channelID, m.s.Me.Id, map[string]string{model.MarkUnreadNotifyProp: mark})
		m.ui(func() {
			if err != nil {
				m.fail("mute channel", fmt.Errorf("set mark_unread=%s in channel %s: %w", mark, channelID, err))
				return
			}
			m.st.SetMuted(channelID, muted)
			m.refreshItem(channelID)
		})
	}()
}

// confirmLeave — «Покинуть канал…»: из приватного без приглашения не вернуться.
func (m *MainWindow) confirmLeave(channelID string) {
	ch := m.st.Channel(channelID)
	if ch == nil {
		return
	}
	d := newDialog(m.w, "Покинуть канал")
	m.channelDialog = d
	text := "Покинуть канал «" + m.st.Title(ch) + "»? Вернуться можно через «Обзор каналов»."
	if ch.Type == model.ChannelTypePrivate {
		text = "Покинуть приватный канал «" + m.st.Title(ch) + "»? Вернуться можно, только если вас снова пригласят."
	}
	label := qt.NewQLabel3(text)
	label.SetWordWrap(true)
	d.lay.AddWidget(label.QWidget)
	danger(d.buttons("Покинуть", func() { m.leaveChannel(channelID) }))
	d.w.Show()
}

func (m *MainWindow) leaveChannel(channelID string) {
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		_, err := m.s.Client.RemoveUserFromChannel(ctx, channelID, m.s.Me.Id)
		m.ui(func() {
			if err != nil {
				m.fail("leave channel", fmt.Errorf("leave channel %s: %w", channelID, err))
				return
			}
			if m.current == channelID {
				m.current = "" // сайдбар откроет канал по умолчанию
			}
			m.reloadSidebar()
		})
	}()
}

// joinChannel — войти в открытый канал и открыть его.
func (m *MainWindow) joinChannel(ch *model.Channel, done func(err error)) {
	if m.items[ch.Id] != nil {
		m.selectChannel(ch.Id)
		done(nil)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		_, _, err := m.s.Client.AddChannelMember(ctx, ch.Id, m.s.Me.Id)
		m.ui(func() {
			if err == nil {
				m.selectAfterLoad = ch.Id
				m.reloadSidebar()
			}
			done(err)
		})
	}()
}

// teamForAction — команда для «Обзор каналов» и «Создать канал» из меню:
// команда открытого канала, иначе первая.
func (m *MainWindow) teamForAction(team string) string {
	if team != "" {
		return team
	}
	return m.teamOf(m.current)
}

// showBrowseChannels — открытые каналы команды: поиск, «Войти».
func (m *MainWindow) showBrowseChannels(team string) {
	team = m.teamForAction(team)
	d := newDialog(m.w, "Обзор каналов")
	m.channelDialog = d
	d.w.SetMinimumSize2(520, 480)
	filter := qt.NewQLineEdit2()
	filter.SetPlaceholderText("Найти канал")
	filter.SetClearButtonEnabled(true)
	list := qt.NewQListWidget2()
	setName(list.QObject, "channelList")
	hint := qt.NewQLabel3("Загружаем…")
	hint.SetProperty("role", qt.NewQVariant11("hint"))
	hint.SetWordWrap(true)
	d.lay.AddWidget(filter.QWidget)
	d.lay.AddWidget2(list.QWidget, 1)
	d.lay.AddWidget(hint.QWidget)

	var all []*model.Channel
	fill := func() {
		q := strings.ToLower(strings.TrimSpace(filter.Text()))
		list.Clear()
		for _, ch := range all {
			if q != "" && !strings.Contains(strings.ToLower(ch.DisplayName+" "+ch.Name+" "+ch.Purpose), q) {
				continue
			}
			text := ch.DisplayName + "\n~" + ch.Name
			if ch.Purpose != "" {
				text += " · " + ch.Purpose
			}
			if m.items[ch.Id] != nil {
				text += " · вы здесь"
			}
			it := qt.NewQListWidgetItem7(text, list)
			it.SetData(int(qt.UserRole), qt.NewQVariant11(ch.Id))
			it.SetToolTip(ch.Purpose)
		}
		if list.Count() > 0 {
			list.SetCurrentRow(0)
		}
	}
	filter.OnTextChanged(func(string) { fill() })
	join := func() {
		it := list.CurrentItem()
		if it == nil {
			return
		}
		id := it.Data(int(qt.UserRole)).ToString()
		i := slices.IndexFunc(all, func(c *model.Channel) bool { return c.Id == id })
		if i < 0 {
			return
		}
		hint.SetText("Входим…")
		m.joinChannel(all[i], func(err error) {
			if d.closed {
				return
			}
			if err != nil {
				hint.SetText("Не вышло: " + commandError(err))
				return
			}
			d.w.Close()
		})
	}
	list.OnItemActivated(func(*qt.QListWidgetItem) { join() })

	create := qt.NewQPushButton3("Создать канал…")
	create.OnClicked(func() {
		d.w.Close()
		m.showCreateChannel(team)
	})
	d.bottom.AddWidget(create.QWidget)
	d.bottom.AddStretch()
	cancel := qt.NewQPushButton3("Закрыть")
	cancel.OnClicked(func() { d.w.Close() })
	ok := qt.NewQPushButton3("Войти")
	ok.SetProperty("primary", qt.NewQVariant11("true"))
	ok.SetDefault(true)
	ok.OnClicked(join)
	d.bottom.AddWidget(cancel.QWidget)
	d.bottom.AddWidget(ok.QWidget)
	d.w.Show()
	filter.SetFocus()

	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		var chs []*model.Channel
		var err error
		for page := 0; page < 10; page++ {
			batch, _, e := m.s.Client.GetPublicChannelsForTeam(ctx, team, page, 200, "")
			if e != nil {
				err = e
				break
			}
			chs = append(chs, batch...)
			if len(batch) < 200 {
				break
			}
		}
		m.ui(func() {
			if d.closed {
				return
			}
			if err != nil {
				hint.SetText("Не удалось загрузить каналы: " + commandError(err))
				return
			}
			slices.SortFunc(chs, func(a, b *model.Channel) int {
				return strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName))
			})
			all = chs
			hint.SetText(fmt.Sprintf("Открытых каналов: %d. Enter или двойной щелчок — войти.", len(all)))
			fill()
		})
	}()
}

// showCreateChannel — новый канал: название, адрес (из названия, можно
// поправить), открытый или приватный, назначение.
func (m *MainWindow) showCreateChannel(team string) {
	team = m.teamForAction(team)
	d := newDialog(m.w, "Создать канал")
	m.channelDialog = d
	name := qt.NewQLineEdit2()
	name.SetPlaceholderText("Например, Релизы")
	addr := qt.NewQLineEdit2()
	addr.SetPlaceholderText("relizy")
	open := qt.NewQRadioButton3("Открытый — найти и войти может любой в команде")
	private := qt.NewQRadioButton3("Приватный — только по приглашению")
	open.SetChecked(true)
	purpose := qt.NewQLineEdit2()
	purpose.SetPlaceholderText("Необязательно")
	hint := qt.NewQLabel2()
	hint.SetProperty("role", qt.NewQVariant11("hint"))
	hint.SetWordWrap(true)
	hint.SetVisible(false)
	for _, w := range []struct {
		label string
		w     *qt.QWidget
	}{{"Название", name.QWidget}, {"Адрес", addr.QWidget}} {
		d.lay.AddWidget(fieldLabel(w.label).QWidget)
		d.lay.AddWidget(w.w)
	}
	d.lay.AddWidget(open.QWidget)
	d.lay.AddWidget(private.QWidget)
	d.lay.AddWidget(fieldLabel("Назначение").QWidget)
	d.lay.AddWidget(purpose.QWidget)
	d.lay.AddWidget(hint.QWidget)

	addrEdited := false
	addr.OnTextEdited(func(string) { addrEdited = true })
	name.OnTextChanged(func(t string) {
		if !addrEdited {
			addr.SetText(store.ChannelURLName(t))
		}
	})

	d.bottom.AddStretch()
	cancel := qt.NewQPushButton3("Отмена")
	cancel.OnClicked(func() { d.w.Close() })
	ok := qt.NewQPushButton3("Создать")
	ok.SetProperty("primary", qt.NewQVariant11("true"))
	ok.SetDefault(true)
	ok.OnClicked(func() {
		display, url := strings.TrimSpace(name.Text()), strings.TrimSpace(addr.Text())
		problem := store.ValidChannelName(url)
		if display == "" {
			problem = "Нужно название канала"
		}
		if problem != "" {
			hint.SetText(problem)
			hint.SetVisible(true)
			return
		}
		typ := model.ChannelTypeOpen
		if private.IsChecked() {
			typ = model.ChannelTypePrivate
		}
		ch := &model.Channel{TeamId: team, Name: url, DisplayName: display, Type: typ, Purpose: strings.TrimSpace(purpose.Text())}
		ok.SetEnabled(false)
		go func() {
			ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
			defer cancel()
			created, _, err := m.s.Client.CreateChannel(ctx, ch)
			m.ui(func() {
				if err != nil {
					m.log.Warn("create channel", "name", url, "err", err)
					if !d.closed {
						ok.SetEnabled(true)
						hint.SetText("Не создан: " + commandError(err))
						hint.SetVisible(true)
					}
					return
				}
				if !d.closed {
					d.w.Close()
				}
				m.selectAfterLoad = created.Id
				m.reloadSidebar()
			})
		}()
	})
	d.bottom.AddWidget(cancel.QWidget)
	d.bottom.AddWidget(ok.QWidget)
	d.w.Show()
	name.SetFocus()
}
