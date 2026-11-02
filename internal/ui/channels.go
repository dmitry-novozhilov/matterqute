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

// menuItem — пункт контекстного меню; title "" — разделитель, sub — подменю.
type menuItem struct {
	title string
	do    func()
	check menuCheck
	sub   []menuItem
}

// menuCheck — галочка у пункта: нет, снята, стоит.
type menuCheck int8

const (
	noCheck menuCheck = iota
	unchecked
	checked
)

func checkIf(on bool) menuCheck {
	if on {
		return checked
	}
	return unchecked
}

// fillMenu — пункты items в menu (с подменю и галочками).
func fillMenu(menu *qt.QMenu, items []menuItem) {
	for _, x := range items {
		switch {
		case x.title == "":
			menu.AddSeparator()
		case x.sub != nil:
			fillMenu(menu.AddMenuWithTitle(x.title), x.sub)
		default:
			a := menu.QWidget.AddActionWithText(x.title)
			if x.check != noCheck {
				a.SetCheckable(true)
				a.SetChecked(x.check == checked)
			}
			a.OnTriggered(x.do)
		}
	}
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
	fillMenu(menu, items)
	menu.ExecWithPos(m.sidebar.Viewport().MapToGlobalWithQPoint(pos))
}

// sidebarItems — что можно сделать с командой team или каналом channelID.
func (m *MainWindow) sidebarItems(team, channelID string) []menuItem {
	var out []menuItem
	if team != "" {
		out = append(out, menuItem{title: "Обзор каналов…", do: func() { m.showBrowseChannels(team) }},
			menuItem{title: "Создать канал…", do: func() { m.showCreateChannel(team) }})
	}
	ch := m.st.Channel(channelID)
	if ch == nil {
		return out
	}
	id := ch.Id
	if ch.Type != model.ChannelTypeDirect {
		out = append(out, menuItem{title: "Участники", do: func() {
			m.selectChannel(id)
			m.showMembers(id)
		}})
	}
	if !ch.IsGroupOrDirect() {
		out = append(out, menuItem{title: "Добавить участников…", do: func() { m.showAddMembers(id) }},
			menuItem{title: "Настройки канала…", do: func() { m.showChannelSettings(id) }})
	} else {
		out = append(out, menuItem{title: "Изменить шапку…", do: func() { m.showChannelSettings(id) }})
	}
	out = append(out, menuItem{})
	if m.st.Muted(id) {
		out = append(out, menuItem{title: "Включить уведомления", do: func() { m.setMuted(id, false) }})
	} else {
		out = append(out, menuItem{title: "Выключить уведомления", do: func() { m.setMuted(id, true) }})
	}
	out = append(out, menuItem{title: "Уведомления на рабочий стол", sub: m.notifyItems(ch)})
	if link := m.channelLink(ch); link != "" {
		out = append(out, menuItem{title: "Копировать ссылку на канал", do: func() {
			qt.QGuiApplication_Clipboard().SetText(link)
			m.message("Ссылка на канал скопирована")
		}})
	}
	// Из главного канала команды сервер не выпускает — как веб-клиент, пункта нет.
	if !ch.IsGroupOrDirect() && ch.Name != model.DefaultChannelName {
		out = append(out, menuItem{}, menuItem{title: "Покинуть канал…", do: func() { m.confirmLeave(id) }})
	}
	return out
}

// channelMenu — щелчок по названию канала в шапке: то же меню, что у канала
// в сайдбаре.
func (m *MainWindow) channelMenu() {
	items := m.sidebarItems("", m.current)
	if len(items) == 0 {
		return
	}
	menu := qt.NewQMenu(m.w)
	defer menu.DeleteLater()
	fillMenu(menu, items)
	menu.ExecWithPos(m.head.title.MapToGlobalWithQPoint(qt.NewQPoint2(0, m.head.title.Height()+4)))
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

// notifyItems — «Уведомления на рабочий стол» канала: уровень (как в
// профиле, все, упоминания, ничего), ответы в тредах (при свёрнутых
// тредах), игнорировать @channel, @here, @all (не в личках), — как окно
// настроек канала в веб-клиенте.
func (m *MainWindow) notifyItems(ch *model.Channel) []menuItem {
	id := ch.Id
	level := m.st.NotifyProp(id, model.DesktopNotifyProp)
	if level == "" {
		level = model.ChannelNotifyDefault
	}
	profile := map[string]string{model.UserNotifyAll: "все", model.UserNotifyMention: "упоминания", model.UserNotifyNone: "ничего"}[m.s.Me.NotifyProps[model.DesktopNotifyProp]]
	if profile == "" {
		profile = "все"
	}
	set := func(key, value string) func() { return func() { m.setNotifyProp(id, key, value) } }
	out := []menuItem{
		{title: "Как в профиле (" + profile + ")", do: set(model.DesktopNotifyProp, model.ChannelNotifyDefault), check: checkIf(level == model.ChannelNotifyDefault)},
		{title: "Все сообщения", do: set(model.DesktopNotifyProp, model.ChannelNotifyAll), check: checkIf(level == model.ChannelNotifyAll)},
		{title: "Только упоминания", do: set(model.DesktopNotifyProp, model.ChannelNotifyMention), check: checkIf(level == model.ChannelNotifyMention)},
		{title: "Ничего", do: set(model.DesktopNotifyProp, model.ChannelNotifyNone), check: checkIf(level == model.ChannelNotifyNone)},
	}
	if m.st.CRT() {
		threads := m.st.NotifyProp(id, model.DesktopThreadsNotifyProp)
		if threads == "" || threads == model.ChannelNotifyDefault {
			threads = m.s.Me.NotifyProps[model.DesktopThreadsNotifyProp]
		}
		on := threads != model.UserNotifyMention // по умолчанию сервер — «все»
		value := model.UserNotifyAll
		if on {
			value = model.UserNotifyMention
		}
		out = append(out, menuItem{}, menuItem{title: "Ответы в тредах, где я участвую", do: set(model.DesktopThreadsNotifyProp, value), check: checkIf(on)})
	}
	if !ch.IsGroupOrDirect() {
		ignore := m.st.NotifyProp(id, model.IgnoreChannelMentionsNotifyProp) == model.IgnoreChannelMentionsOn
		value := model.IgnoreChannelMentionsOn
		if ignore {
			value = model.IgnoreChannelMentionsOff
		}
		out = append(out, menuItem{}, menuItem{title: "Игнорировать @channel, @here, @all", do: set(model.IgnoreChannelMentionsNotifyProp, value), check: checkIf(ignore)})
	}
	return out
}

// setMuted — выключить (muted) или включить уведомления канала: как «Mute»
// веб-клиента, mark_unread.
func (m *MainWindow) setMuted(channelID string, muted bool) {
	mark := model.ChannelMarkUnreadAll
	if muted {
		mark = model.ChannelMarkUnreadMention
	}
	m.setNotifyProp(channelID, model.MarkUnreadNotifyProp, mark)
}

// setNotifyProp — настройка уведомлений канала: на сервер, по ответу — в
// store (потом то же придёт событием channel_member_updated).
func (m *MainWindow) setNotifyProp(channelID, key, value string) {
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		_, err := m.s.Client.UpdateChannelNotifyProps(ctx, channelID, m.s.Me.Id, map[string]string{key: value})
		m.ui(func() {
			if err != nil {
				m.fail("channel notifications", fmt.Errorf("set %s=%s in channel %s: %w", key, value, channelID, err))
				return
			}
			m.st.SetNotifyProp(channelID, key, value)
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
			// Сразу, без ожидания перезагрузки сайдбара (на настоящем сервере
			// это несколько запросов): канал пропадает, открыт — вместо него
			// канал по умолчанию.
			if m.current == channelID {
				m.current = ""
			}
			if t := m.thread; t != nil && t.channelID == channelID {
				m.closeThread()
			}
			m.st.RemoveChannel(channelID)
			m.dropFeed(channelID)
			m.fillSidebar()
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
		member, _, err := m.s.Client.AddChannelMember(ctx, ch.Id, m.s.Me.Id)
		m.ui(func() {
			if err == nil {
				m.addChannel(ch, member)
			}
			done(err)
		})
	}()
}

// addChannel — вошли в канал или создали его: в сайдбар и открыть сразу,
// счётчики и остальное — перезагрузкой сайдбара следом.
func (m *MainWindow) addChannel(ch *model.Channel, member *model.ChannelMember) {
	m.st.AddChannel(ch, member)
	m.selectAfterLoad = ch.Id
	m.fillSidebar()
	m.reloadSidebar()
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
				m.addChannel(created, nil)
			})
		}()
	})
	d.bottom.AddWidget(cancel.QWidget)
	d.bottom.AddWidget(ok.QWidget)
	d.w.Show()
	name.SetFocus()
}

// showChannelSettings — название, адрес, назначение и шапка канала (у
// личек — только шапка): серверу — изменённое (PatchChannel), отказ — под
// полями. Адрес главного канала команды не меняется.
func (m *MainWindow) showChannelSettings(channelID string) {
	ch := m.st.Channel(channelID)
	if ch == nil {
		return
	}
	group := ch.IsGroupOrDirect()
	title := "Настройки канала"
	if group {
		title = "Шапка " + channelPrefix(ch) + m.st.Title(ch)
	}
	d := newDialog(m.w, title)
	m.channelDialog = d
	d.w.SetMinimumWidth(460)
	name := qt.NewQLineEdit3(ch.DisplayName)
	addr := qt.NewQLineEdit3(ch.Name)
	if ch.Name == model.DefaultChannelName {
		addr.SetEnabled(false)
		addr.SetToolTip("Адрес главного канала команды не меняется")
	}
	purpose := qt.NewQLineEdit3(ch.Purpose)
	purpose.SetPlaceholderText("Необязательно")
	head := qt.NewQPlainTextEdit3(ch.Header)
	head.SetTabChangesFocus(true)
	head.SetPlaceholderText("Ссылки, @упоминания, разметка — как в сообщениях")
	head.SetFixedHeight(head.FontMetrics().LineSpacing()*4 + 12)
	hint := qt.NewQLabel2()
	hint.SetProperty("role", qt.NewQVariant11("hint"))
	hint.SetWordWrap(true)
	hint.SetVisible(false)
	if !group {
		for _, f := range []struct {
			label string
			w     *qt.QWidget
		}{{"Название", name.QWidget}, {"Адрес", addr.QWidget}, {"Назначение", purpose.QWidget}} {
			d.lay.AddWidget(fieldLabel(f.label).QWidget)
			d.lay.AddWidget(f.w)
		}
	}
	d.lay.AddWidget(fieldLabel("Шапка").QWidget)
	d.lay.AddWidget(head.QWidget)
	d.lay.AddWidget(hint.QWidget)

	d.bottom.AddStretch()
	cancel := qt.NewQPushButton3("Отмена")
	cancel.OnClicked(func() { d.w.Close() })
	ok := qt.NewQPushButton3("Сохранить")
	ok.SetProperty("primary", qt.NewQVariant11("true"))
	ok.SetDefault(true)
	ok.OnClicked(func() {
		var patch model.ChannelPatch
		changed := false
		set := func(field **string, now, was string) {
			if now != was {
				*field = &now
				changed = true
			}
		}
		problem := ""
		if !group {
			display, url := strings.TrimSpace(name.Text()), strings.TrimSpace(addr.Text())
			if url != ch.Name {
				problem = store.ValidChannelName(url)
			}
			if display == "" {
				problem = "Нужно название канала"
			}
			set(&patch.DisplayName, display, ch.DisplayName)
			set(&patch.Name, url, ch.Name)
			set(&patch.Purpose, strings.TrimSpace(purpose.Text()), ch.Purpose)
		}
		set(&patch.Header, strings.TrimSpace(head.ToPlainText()), ch.Header)
		if problem != "" {
			hint.SetText(problem)
			hint.SetVisible(true)
			return
		}
		if !changed {
			d.w.Close()
			return
		}
		ok.SetEnabled(false)
		go func() {
			ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
			defer cancel()
			updated, _, err := m.s.Client.PatchChannel(ctx, channelID, &patch)
			m.ui(func() {
				if err != nil {
					m.log.Warn("patch channel", "channel", channelID, "err", err)
					if !d.closed {
						ok.SetEnabled(true)
						hint.SetText("Не сохранено: " + commandError(err))
						hint.SetVisible(true)
					}
					return
				}
				if !d.closed {
					d.w.Close()
				}
				if m.st.Channel(channelID) == nil {
					return // пока сохраняли, из канала вышли
				}
				m.st.AddChannel(updated, nil)
				if channelID == m.current {
					m.composer.SetPlaceholder("Сообщение в " + channelPrefix(updated) + m.st.Title(updated))
				}
				m.fillSidebar() // название — и порядок в сайдбаре
			})
		}()
	})
	d.bottom.AddWidget(cancel.QWidget)
	d.bottom.AddWidget(ok.QWidget)
	d.w.Show()
	if group {
		head.SetFocus()
	} else {
		name.SetFocus()
	}
}
