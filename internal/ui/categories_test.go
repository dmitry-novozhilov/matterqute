package ui

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// Сайдбар по категориям: команды строками-заголовками, лички в конце;
// избранное, перетаскивание с ручной сортировкой, свёрнутая категория,
// своя категория, «отметить прочитанным» — и всё это на сервере.
func TestSidebarCategories(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		ctx := context.Background()
		me := m.s.Me.Id
		dev := m.st.Channel(m.current).TeamId
		town := m.current
		backend := m.st.ChannelByName("backend", dev).Id
		releases := m.st.ChannelByName("releases", dev).Id

		// Строки сайдбара: «#команда», «группа:каналы через запятую».
		layout := func() string {
			var out []string
			for i := range m.sidebar.TopLevelItemCount() {
				g := m.sidebar.TopLevelItem(i)
				if team := g.Data(0, teamLabelRole).ToString(); team != "" {
					out = append(out, "#"+team)
					continue
				}
				title := g.Data(0, groupRole).ToString()
				if title == "" {
					continue // «Треды»
				}
				var chs []string
				for j := range g.ChildCount() {
					c := g.Child(j)
					name := m.st.Title(m.st.Channel(c.Data(0, channelIDRole).ToString()))
					if c.IsHidden() {
						name = "(" + name + ")"
					}
					chs = append(chs, name)
				}
				out = append(out, title+":"+strings.Join(chs, ","))
			}
			return strings.Join(out, " ")
		}
		if got := layout(); !strings.HasPrefix(got, "#Разработка Каналы:backend,Town Square,Релизы #Эксплуатация Каналы:alerts,Town Square Личные сообщения:") {
			t.Errorf("layout %s", got)
		}
		group := func(team, title string) *qt.QTreeWidgetItem {
			for i := range m.sidebar.TopLevelItemCount() {
				if g := m.sidebar.TopLevelItem(i); g.Data(0, teamRole).ToString() == team && g.Data(0, groupRole).ToString() == title {
					return g
				}
			}
			return nil
		}
		server := func(kind model.SidebarCategoryType) *model.SidebarCategoryWithChannels {
			cats, _, err := s.Client.GetSidebarCategoriesForTeamForUser(ctx, me, dev, "")
			if err != nil {
				t.Errorf("categories: %v", err)
				return &model.SidebarCategoryWithChannels{}
			}
			for _, c := range cats.Categories {
				if c.Type == kind {
					return c
				}
			}
			return &model.SidebarCategoryWithChannels{}
		}
		do := func(items []menuItem, title string) {
			for _, x := range items {
				if x.title == title {
					x.do()
					return
				}
				for _, y := range x.sub {
					if y.title == title {
						y.do()
						return
					}
				}
			}
			t.Errorf("no %q in menu", title)
		}

		// В избранное — группа появилась, на сервере тоже.
		do(m.sidebarItems("", backend), "В избранное")
		if !strings.Contains(layout(), "#Разработка Избранное:backend Каналы:Town Square,Релизы") {
			t.Errorf("after favorite: %s", layout())
		}
		if !waitFor(t, "favorite on server", func() bool { return slices.Equal(server(model.SidebarCategoryFavorites).Channels, []string{backend}) }) {
			return
		}
		shot(t, m, "sidebar-categories")

		// Перетащить «Релизы» выше Town Square: как отпустить над верхом её строки.
		m.sidebar.SetCurrentItem(m.items[releases])
		r := m.sidebar.VisualItemRect(m.items[town])
		team, id, cat, order, ok := m.dropTarget(qt.NewQPoint2(r.X()+10, r.Y()+2))
		if !ok || team != dev || id != releases || !slices.Equal(order, []string{releases, town}) {
			t.Errorf("drop target: %v %s %s %v", ok, team, cat, order)
			return
		}
		m.moveChannel(team, id, cat, order)
		if !strings.Contains(layout(), "Каналы:Релизы,Town Square") {
			t.Errorf("after drag: %s", layout())
		}
		if !waitFor(t, "manual order on server", func() bool {
			c := server(model.SidebarCategoryChannels)
			return c.Sorting == model.SidebarCategorySortManual && slices.Equal(c.Channels, []string{releases, town})
		}) {
			return
		}
		// Личку в «Каналы» не кладут.
		for _, ch := range m.st.DirectChannels() {
			m.sidebar.SetCurrentItem(m.items[ch.Id])
			if _, _, _, _, ok := m.dropTarget(qt.NewQPoint2(r.X()+10, r.Y()+2)); ok {
				t.Error("DM may be dropped into Channels")
			}
			break
		}
		m.sidebar.SetCurrentItem(m.items[town])

		// Сортировка по алфавиту обратно — из меню категории.
		chans := group(dev, "Каналы").Data(0, categoryRole).ToString()
		do(m.categoryItems(dev, chans), "По алфавиту")
		if !strings.Contains(layout(), "Каналы:Town Square,Релизы") {
			t.Errorf("after alpha: %s", layout())
		}

		// Свернуть «Каналы»: виден только открытый (Town Square) и непрочитанное.
		m.toggleCategory(group(dev, "Каналы"))
		if !strings.Contains(layout(), "Каналы:Town Square,(Релизы)") {
			t.Errorf("collapsed: %s", layout())
		}
		if !waitFor(t, "collapsed on server", func() bool { return server(model.SidebarCategoryChannels).Collapsed }) {
			return
		}

		// Своя категория с каналом, переименовать, удалить.
		m.newCategory(dev, releases)
		edit := qt.UnsafeNewQLineEdit(func() *qt.QObject {
			for _, o := range descendants(m.channelDialog.w.QObject) {
				if o.Inherits("QLineEdit") {
					return o
				}
			}
			return nil
		}().UnsafePointer())
		edit.SetText("Проекты")
		labelled(m.channelDialog.w.QObject, "Создать").Click()
		if !waitFor(t, "custom category", func() bool { return strings.Contains(layout(), "Проекты:Релизы") }) {
			return
		}
		custom := group(dev, "Проекты").Data(0, categoryRole).ToString()
		m.renameCategory(dev, custom, "Работа")
		if !waitFor(t, "renamed on server", func() bool { return server(model.SidebarCategoryCustom).DisplayName == "Работа" }) {
			return
		}
		m.confirmDeleteCategory(dev, custom)
		labelled(m.channelDialog.w.QObject, "Удалить").Click()
		if !waitFor(t, "category deleted", func() bool {
			return !strings.Contains(layout(), "Работа") && strings.Contains(layout(), "Релизы")
		}) {
			return
		}

		// «Отметить всё прочитанным»: каналы и треды — и на сервере.
		m.markAllRead()
		if c := m.counts; c.Mentions != 0 || c.Unread != 0 {
			t.Errorf("counts after mark all read: %+v", c)
		}
		if unread, _ := m.st.ThreadsUnread(); unread {
			t.Error("threads still unread")
		}
		if !waitFor(t, "read on server", func() bool {
			mm, _, err := s.Client.GetChannelMember(ctx, backend, me, "")
			return err == nil && mm.MsgCount == m.st.Channel(backend).TotalMsgCount && mm.MsgCount > 0
		}) {
			return
		}
	})
}
