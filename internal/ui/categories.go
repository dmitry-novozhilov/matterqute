package ui

import (
	"context"
	"fmt"
	"strings"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/store"
)

// Категории сайдбара, как в веб-клиенте: избранное, каналы, личные
// сообщения и свои; у каждой — сортировка (по алфавиту, по последней
// активности, вручную) и свёрнутость. Хранит сервер, раскладывает
// store.Sections. Правый щелчок по категории — «Отметить прочитанным»,
// сортировка, новая (своя — переименовать, удалить); по каналу — «В
// избранное», «Переместить в». Каналы перетаскиваются мышью (sidebar.go).
// Изменения — сразу в сайдбаре, следом — на сервер; не вышло — категории
// с сервера заново.

// toggleCategory — щелчок по группе: свернуть или развернуть (у категории — и на сервере).
func (m *MainWindow) toggleCategory(g *qt.QTreeWidgetItem) {
	collapsed := !g.Data(0, collapsedRole).ToBool()
	g.SetData(0, collapsedRole, qt.NewQVariant8(collapsed))
	m.applyCollapsed()
	m.sidebar.Viewport().Update()
	team, id := g.Data(0, teamRole).ToString(), g.Data(0, categoryRole).ToString()
	if c := m.st.UpdateCategory(team, id, func(c *model.SidebarCategoryWithChannels) { c.Collapsed = collapsed }); c != nil {
		m.saveCategories(team, c)
	}
}

// dropTarget — куда ляжет перетаскиваемый канал (выделенная строка), если
// отпустить в pos: команда, категория и её каналы в новом порядке; ok=false —
// туда нельзя (не категория, чужая команда, лички в «Каналы» и наоборот).
func (m *MainWindow) dropTarget(pos *qt.QPoint) (team, id, cat string, order []string, ok bool) {
	cur, over := m.sidebar.CurrentItem(), m.sidebar.ItemAt(pos)
	if cur == nil || over == nil {
		return
	}
	id = cur.Data(0, channelIDRole).ToString()
	ch := m.st.Channel(id)
	if ch == nil {
		return
	}
	group, at := over, 0
	if over.Data(0, channelIDRole).ToString() != "" {
		if group = over.Parent(); group == nil {
			return
		}
		at = group.IndexOfChild(over)
		if r := m.sidebar.VisualItemRect(over); pos.Y() > r.Y()+r.Height()/2 {
			at++
		}
	}
	team, cat = group.Data(0, teamRole).ToString(), group.Data(0, categoryRole).ToString()
	c := m.st.Category(team, cat)
	if c == nil || m.st.CategoryTeam(id) != team || !store.Accepts(c, ch) {
		return "", "", "", nil, false
	}
	for j := range group.ChildCount() {
		if j == at {
			order = append(order, id)
		}
		if cid := group.Child(j).Data(0, channelIDRole).ToString(); cid != id {
			order = append(order, cid)
		}
	}
	if at >= group.ChildCount() {
		order = append(order, id)
	}
	return team, id, cat, order, true
}

// moveChannel — канал в категорию to (order — её каналы в новом порядке,
// nil — канал в начало): сайдбар сразу, сервер следом.
func (m *MainWindow) moveChannel(team, channelID, to string, order []string) {
	changed := m.st.MoveChannel(team, channelID, to, order)
	m.fillSidebar()
	if changed != nil {
		m.saveCategories(team, changed...)
	}
}

// saveCategories — изменённые категории команды на сервер. По одному
// запросу на команду: параллельные PUT приходили не по порядку, и старая
// копия затирала новую (сортировка, следом — свёрнутость). Пока запрос в
// пути, изменения копятся (у категории — последняя копия) и уходят следом.
func (m *MainWindow) saveCategories(team string, cats ...*model.SidebarCategoryWithChannels) {
	q := m.catPending[team]
	if q == nil {
		q = map[string]*model.SidebarCategoryWithChannels{}
		m.catPending[team] = q
	}
	for _, c := range cats {
		q[c.Id] = c
	}
	if !m.catSaving[team] {
		m.flushCategories(team)
	}
}

// flushCategories — накопленное на сервер; очередь пуста — категории с
// сервера заново, если о них просили, пока сохраняли.
func (m *MainWindow) flushCategories(team string) {
	q := m.catPending[team]
	delete(m.catPending, team)
	if len(q) == 0 {
		delete(m.catSaving, team)
		if m.catReloadAfter[team] {
			delete(m.catReloadAfter, team)
			m.reloadCategories(team)
		}
		return
	}
	m.catSaving[team] = true
	cats := make([]*model.SidebarCategoryWithChannels, 0, len(q))
	for _, c := range q {
		cats = append(cats, c)
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		_, _, err := m.s.Client.UpdateSidebarCategoriesForTeamForUser(ctx, m.s.Me.Id, team, cats)
		m.ui(func() {
			if err != nil {
				m.fail("update sidebar categories", fmt.Errorf("update %d sidebar categories in team %s: %w", len(cats), team, err))
				m.catReloadAfter[team] = true
			}
			m.flushCategories(team)
		})
	}()
}

// reloadCategories — категории команды с сервера (события sidebar_category_*,
// отказ в изменении). Пока запрос идёт, новые просьбы копятся в один повтор;
// пока свои изменения не дошли до сервера, ответ устарел бы — ждём их.
func (m *MainWindow) reloadCategories(team string) {
	if m.catSaving[team] {
		m.catReloadAfter[team] = true
		return
	}
	if m.catLoading[team] > 0 {
		m.catLoading[team] = 2
		return
	}
	m.catLoading[team] = 1
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		cats, _, err := m.s.Client.GetSidebarCategoriesForTeamForUser(ctx, m.s.Me.Id, team, "")
		m.ui(func() {
			again := m.catLoading[team] == 2
			delete(m.catLoading, team)
			switch {
			case m.catSaving[team]:
				m.catReloadAfter[team] = true // пока ехал ответ, начали сохранять
			case err != nil:
				m.log.Warn("load sidebar categories", "team", team, "err", err)
			default:
				m.st.SetCategories(team, cats)
				m.fillSidebar()
			}
			if again {
				m.reloadCategories(team)
			}
		})
	}()
}

// channelCategoryItems — «В избранное» и «Переместить в» у канала; пусто —
// у его команды нет категорий.
func (m *MainWindow) channelCategoryItems(ch *model.Channel) []menuItem {
	team, id := m.st.CategoryTeam(ch.Id), ch.Id
	cats := m.st.Categories(team)
	if cats == nil {
		return nil
	}
	var out []menuItem
	if m.st.Favorite(id) {
		if def := m.st.DefaultCategory(id); def != nil {
			out = append(out, menuItem{title: "Убрать из избранного", do: func() { m.moveChannel(team, id, def.Id, nil) }})
		}
	} else if fav := m.st.FavoritesCategory(id); fav != nil {
		out = append(out, menuItem{title: "В избранное", do: func() { m.moveChannel(team, id, fav.Id, nil) }})
	}
	var to []menuItem
	cur := m.st.CategoryOf(id)
	for _, c := range cats {
		if c == cur || !store.Accepts(c, ch) {
			continue
		}
		to = append(to, menuItem{title: store.CategoryTitle(c), do: func() { m.moveChannel(team, id, c.Id, nil) }})
	}
	if len(to) > 0 {
		to = append(to, menuItem{})
	}
	to = append(to, menuItem{title: "Новая категория…", do: func() { m.newCategory(team, id) }})
	return append(out, menuItem{title: "Переместить в", sub: to})
}

// categoryItems — меню категории: прочитать, сортировка, своя — переименовать
// и удалить, новая; у «Каналов» — обзор и создание, у личек — новое сообщение.
func (m *MainWindow) categoryItems(team, catID string) []menuItem {
	c := m.st.Category(team, catID)
	if c == nil {
		return nil
	}
	var out []menuItem
	if ids := m.unreadIn(team, catID); len(ids) > 0 {
		out = append(out, menuItem{title: "Отметить прочитанным", do: func() { m.markRead(ids) }}, menuItem{})
	}
	sorting := store.Sorting(c)
	sortItem := func(title string, s model.SidebarCategorySorting) menuItem {
		return menuItem{title: title, check: checkIf(sorting == s), do: func() { m.setSorting(team, catID, s) }}
	}
	out = append(out, menuItem{title: "Сортировка", sub: []menuItem{
		sortItem("По алфавиту", model.SidebarCategorySortAlphabetical),
		sortItem("По последней активности", model.SidebarCategorySortRecent),
		sortItem("Вручную", model.SidebarCategorySortManual),
	}})
	if c.Type == model.SidebarCategoryCustom {
		name := c.DisplayName
		out = append(out,
			menuItem{title: "Переименовать…", do: func() {
				m.askText("Переименовать категорию", "Название", name, "Сохранить", func(n string) { m.renameCategory(team, catID, n) })
			}},
			menuItem{title: "Удалить категорию…", do: func() { m.confirmDeleteCategory(team, catID) }})
	}
	out = append(out, menuItem{}, menuItem{title: "Новая категория…", do: func() { m.newCategory(team, "") }})
	switch c.Type {
	case model.SidebarCategoryChannels:
		out = append(out, menuItem{}, menuItem{title: "Обзор каналов…", do: func() { m.showBrowseChannels(team) }},
			menuItem{title: "Создать канал…", do: func() { m.showCreateChannel(team) }})
	case model.SidebarCategoryDirectMessages:
		out = append(out, menuItem{}, menuItem{title: "Новое личное сообщение…", do: func() { m.showNewMessage() }})
	}
	return out
}

// teamLabelItems — меню строки команды (при нескольких командах).
func (m *MainWindow) teamLabelItems(team string) []menuItem {
	out := m.sidebarItems(team, "")
	if m.st.Categories(team) != nil {
		out = append(out, menuItem{title: "Новая категория…", do: func() { m.newCategory(team, "") }})
	}
	return out
}

// unreadIn — непрочитанные каналы категории.
func (m *MainWindow) unreadIn(team, catID string) []string {
	var out []string
	for _, sec := range m.st.Sections() {
		if sec.Team != team || sec.Category == nil || sec.Category.Id != catID {
			continue
		}
		for _, id := range sec.Channels {
			if unread, mentions := m.st.Unread(id); unread || mentions > 0 || m.manualUnread[id] {
				out = append(out, id)
			}
		}
	}
	return out
}

func (m *MainWindow) setSorting(team, catID string, s model.SidebarCategorySorting) {
	if c := m.st.UpdateCategory(team, catID, func(c *model.SidebarCategoryWithChannels) { c.Sorting = s }); c != nil {
		m.fillSidebar()
		m.saveCategories(team, c)
	}
}

func (m *MainWindow) renameCategory(team, catID, name string) {
	if c := m.st.UpdateCategory(team, catID, func(c *model.SidebarCategoryWithChannels) { c.DisplayName = name }); c != nil {
		m.fillSidebar()
		m.saveCategories(team, c)
	}
}

// newCategory — своя категория в команде; channelID — сразу переложить в неё канал.
func (m *MainWindow) newCategory(team, channelID string) {
	m.askText("Новая категория", "Название", "", "Создать", func(name string) {
		c := &model.SidebarCategoryWithChannels{
			SidebarCategory: model.SidebarCategory{UserId: m.s.Me.Id, TeamId: team, DisplayName: name, Type: model.SidebarCategoryCustom},
			Channels:        []string{},
		}
		if channelID != "" {
			c.Channels = []string{channelID}
		}
		go func() {
			ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
			defer cancel()
			_, _, err := m.s.Client.CreateSidebarCategoryForTeamForUser(ctx, m.s.Me.Id, team, c)
			m.ui(func() {
				if err != nil {
					m.fail("create sidebar category", fmt.Errorf("create category %q in team %s: %w", name, team, err))
					return
				}
				m.reloadCategories(team)
			})
		}()
	})
}

// confirmDeleteCategory — «Удалить категорию…»: каналы из неё сервер вернёт
// в «Каналы» и «Личные сообщения».
func (m *MainWindow) confirmDeleteCategory(team, catID string) {
	c := m.st.Category(team, catID)
	if c == nil {
		return
	}
	d := newDialog(m.w, "Удалить категорию")
	m.channelDialog = d
	label := qt.NewQLabel3("Удалить категорию «" + c.DisplayName + "»? Каналы из неё вернутся в «Каналы» и «Личные сообщения».")
	label.SetWordWrap(true)
	d.lay.AddWidget(label.QWidget)
	danger(d.buttons("Удалить", func() {
		go func() {
			ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
			defer cancel()
			_, err := m.s.Client.DeleteSidebarCategoryForTeamForUser(ctx, m.s.Me.Id, team, catID)
			m.ui(func() {
				if err != nil {
					m.fail("delete sidebar category", fmt.Errorf("delete category %s in team %s: %w", catID, team, err))
				}
				m.reloadCategories(team)
			})
		}()
	}))
	d.w.Show()
}

// askText — окно с одной строкой: do получает непустой текст.
func (m *MainWindow) askText(title, label, initial, ok string, do func(string)) {
	d := newDialog(m.w, title)
	m.channelDialog = d
	field := qt.NewQLineEdit3(initial)
	d.lay.AddWidget(fieldLabel(label).QWidget)
	d.lay.AddWidget(field.QWidget)
	okb := d.buttons(ok, func() {
		if t := strings.TrimSpace(field.Text()); t != "" {
			do(t)
		}
	})
	okb.SetEnabled(strings.TrimSpace(initial) != "")
	field.OnTextChanged(func(t string) { okb.SetEnabled(strings.TrimSpace(t) != "") })
	d.w.Show()
	field.SetFocus()
	field.SelectAll()
}

// markRead — каналы прочитаны: сразу в сайдбаре, на сервер — одним запросом.
func (m *MainWindow) markRead(ids []string) {
	if len(ids) == 0 {
		return
	}
	for _, id := range ids {
		m.st.MarkViewed(id)
		delete(m.manualUnread, id)
	}
	m.refreshItem("")
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		_, _, err := m.s.Client.ReadMultipleChannels(ctx, m.s.Me.Id, ids)
		m.ui(func() {
			if err != nil {
				m.fail("mark channels read", fmt.Errorf("mark %d channels read: %w", len(ids), err))
				m.reloadSidebar()
			}
		})
	}()
}

// markAllRead — «Отметить всё прочитанным»: все каналы и лички, при
// свёрнутых тредах — и треды.
func (m *MainWindow) markAllRead() {
	var ids []string
	for _, sec := range m.st.Sections() {
		for _, id := range sec.Channels {
			if unread, mentions := m.st.Unread(id); unread || mentions > 0 || m.manualUnread[id] {
				ids = append(ids, id)
			}
		}
	}
	m.markRead(ids)
	if m.st.CRT() {
		m.markThreadsRead()
	}
}

// markThreadsRead — все треды прочитаны (во всех командах).
func (m *MainWindow) markThreadsRead() {
	m.st.MarkThreadsRead()
	m.refreshThreads()
	teams := m.st.Teams()
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		for _, t := range teams {
			if _, err := m.s.Client.UpdateThreadsReadForUser(ctx, m.s.Me.Id, t.Id); err != nil {
				m.ui(func() { m.fail("mark threads read", fmt.Errorf("mark threads read in team %s: %w", t.Name, err)) })
			}
		}
	}()
}
