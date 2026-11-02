package store

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func category(id string, kind model.SidebarCategoryType, sorting model.SidebarCategorySorting, name string, channels ...string) *model.SidebarCategoryWithChannels {
	return &model.SidebarCategoryWithChannels{
		SidebarCategory: model.SidebarCategory{Id: id, Type: kind, Sorting: sorting, DisplayName: name},
		Channels:        channels,
	}
}

// sidebarStore — две команды: в dev каналы a (Альфа), b (Бета), z (Зета),
// в ops — o; лички d1 (свежая), d2.
func sidebarStore(t *testing.T) *Store {
	t.Helper()
	me := &model.User{Id: "me", Username: "me"}
	s := New(me)
	dev, ops := &model.Team{Id: "dev", DisplayName: "Разработка"}, &model.Team{Id: "ops", DisplayName: "Эксплуатация"}
	ch := func(id, team, name string, typ model.ChannelType, last int64) *model.Channel {
		return &model.Channel{Id: id, TeamId: team, DisplayName: name, Name: id, Type: typ, LastPostAt: last}
	}
	s.SetSidebar(&Sidebar{
		Teams: []*model.Team{dev, ops},
		Channels: []*model.Channel{
			ch("a", "dev", "Альфа", model.ChannelTypeOpen, 1), ch("b", "dev", "Бета", model.ChannelTypeOpen, 3),
			ch("z", "dev", "Зета", model.ChannelTypeOpen, 2), ch("o", "ops", "Опс", model.ChannelTypeOpen, 1),
			ch("d1", "", "me, bob", model.ChannelTypeGroup, 9), ch("d2", "", "me, carol", model.ChannelTypeGroup, 5),
		},
		Categories: map[string]*model.OrderedSidebarCategories{
			"dev": {
				Categories: model.SidebarCategoriesWithChannels{
					category("fav", model.SidebarCategoryFavorites, "", "Favorites", "z"),
					category("chan", model.SidebarCategoryChannels, "", "Channels", "b", "a"),
					category("dm", model.SidebarCategoryDirectMessages, "", "Direct Messages", "d2", "d1"),
					category("my", model.SidebarCategoryCustom, model.SidebarCategorySortManual, "Моё"),
				},
				Order: model.SidebarCategoryOrder{"fav", "my", "chan", "dm"},
			},
			"ops": {
				Categories: model.SidebarCategoriesWithChannels{
					category("ofav", model.SidebarCategoryFavorites, "", "Favorites", "d2"),
					category("ochan", model.SidebarCategoryChannels, "", "Channels", "o"),
					category("odm", model.SidebarCategoryDirectMessages, "", "Direct Messages", "d1"),
				},
				Order: model.SidebarCategoryOrder{"ofav", "ochan", "odm"},
			},
		},
	})
	return s
}

func sections(s *Store) string {
	var out []string
	for _, sec := range s.Sections() {
		out = append(out, sec.Team+"/"+sec.Title+":"+strings.Join(sec.Channels, ","))
	}
	return strings.Join(out, " | ")
}

func TestSections(t *testing.T) {
	s := sidebarStore(t)
	// Каналы — по алфавиту, лички — свежие выше; лички только из первой
	// команды, их категория — в конце; пустое избранное ops не показано.
	want := "dev/Избранное:z | dev/Моё: | dev/Каналы:a,b | ops/Каналы:o | dev/Личные сообщения:d1,d2"
	if got := sections(s); got != want {
		t.Errorf("sections\n got %s\nwant %s", got, want)
	}

	// Новый канал, которого нет в категориях, — в «Каналы».
	s.AddChannel(&model.Channel{Id: "c", TeamId: "dev", DisplayName: "Гамма", Type: model.ChannelTypeOpen}, nil)
	if got := sections(s); !strings.Contains(got, "dev/Каналы:a,b,c") {
		t.Errorf("orphan: %s", got)
	}

	// Одна команда — лички на своём месте в порядке категорий.
	s.teams = s.teams[:1]
	if got := sections(s); got != "dev/Избранное:z | dev/Моё: | dev/Каналы:a,b,c | dev/Личные сообщения:d1,d2" {
		t.Errorf("single team: %s", got)
	}

	// Без категорий — по-старому: группа команды и лички.
	s.SetCategories("dev", nil)
	if got := sections(s); got != "dev/Разработка:a,b,c,z | /Личные сообщения:d1,d2" {
		t.Errorf("no categories: %s", got)
	}
}

func TestMoveChannel(t *testing.T) {
	s := sidebarStore(t)
	// В избранное — в начало, из «Каналов» прочь.
	changed := s.MoveChannel("dev", "a", "fav", nil)
	if len(changed) != 2 || strings.Join(changed[0].Channels, ",") != "a,z" || strings.Join(changed[1].Channels, ",") != "b" {
		t.Fatalf("to favorites: %+v", changed)
	}
	if !s.Favorite("a") || s.Favorite("b") {
		t.Error("Favorite")
	}
	// Копии: правка отправленного не трогает своё.
	changed[0].Channels[0] = "x"
	if s.Category("dev", "fav").Channels[0] != "a" {
		t.Error("MoveChannel returned the store's own category")
	}
	// Перестановка внутри — ручная сортировка.
	changed = s.MoveChannel("dev", "z", "fav", []string{"z", "a"})
	if len(changed) != 1 || changed[0].Sorting != model.SidebarCategorySortManual || strings.Join(s.Category("dev", "fav").Channels, ",") != "z,a" {
		t.Errorf("reorder: %+v", changed)
	}
	if got := sections(s); !strings.HasPrefix(got, "dev/Избранное:z,a |") {
		t.Errorf("manual order not kept: %s", got)
	}
	// Хранимый порядок «Каналов» — b,a, показан по алфавиту a,b: перетащить
	// b вверх — тот же хранимый порядок, но сортировка становится ручной.
	if changed := s.MoveChannel("dev", "b", "chan", []string{"b"}); len(changed) != 1 || changed[0].Sorting != model.SidebarCategorySortManual {
		t.Errorf("reorder equal to stored: %+v", changed)
	}
	// Лички в «Каналы» и каналы в «Личные сообщения» не кладут; канал
	// чужой команды — тоже.
	if s.MoveChannel("dev", "d1", "chan", nil) != nil || s.MoveChannel("dev", "b", "dm", nil) != nil || s.MoveChannel("ops", "b", "ochan", nil) != nil {
		t.Error("incompatible move allowed")
	}
	// Личку — в свою категорию: в первой команде.
	if s.MoveChannel("dev", "d1", "my", nil) == nil || s.CategoryOf("d1").Id != "my" {
		t.Error("DM to custom category")
	}
	if d := s.DefaultCategory("d1"); d == nil || d.Id != "dm" {
		t.Errorf("default category of DM: %+v", d)
	}
}
