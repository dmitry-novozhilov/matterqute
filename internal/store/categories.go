package store

import (
	"cmp"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// Категории сайдбара — как в веб-клиенте: у каждой команды свои (избранное,
// каналы, личные сообщения и созданные пользователем), со своим порядком
// каналов, сортировкой и свёрнутостью; хранит их сервер. Лички и групповые
// лежат в категориях каждой команды — показываем их по категориям первой.

// Section — категория в сайдбаре: что и в каком порядке показать.
type Section struct {
	Team      string                             // команда категории
	Category  *model.SidebarCategoryWithChannels // nil — категорий с сервера нет: группа по-старому
	Title     string
	Channels  []string // по порядку показа
	Collapsed bool
}

// SetCategories — категории команды с сервера; nil — не загрузились
// (сайдбар команды — группой по-старому).
func (s *Store) SetCategories(teamID string, c *model.OrderedSidebarCategories) {
	if c == nil {
		delete(s.categories, teamID)
		return
	}
	s.categories[teamID] = c
}

// Categories — категории команды по порядку; nil — их нет.
func (s *Store) Categories(teamID string) []*model.SidebarCategoryWithChannels {
	c := s.categories[teamID]
	if c == nil {
		return nil
	}
	byID := map[string]*model.SidebarCategoryWithChannels{}
	for _, x := range c.Categories {
		byID[x.Id] = x
	}
	var out []*model.SidebarCategoryWithChannels
	seen := map[string]bool{}
	for _, id := range c.Order {
		if x := byID[id]; x != nil && !seen[id] {
			out = append(out, x)
			seen[id] = true
		}
	}
	for _, x := range c.Categories { // не попавшие в порядок — в конец
		if !seen[x.Id] {
			out = append(out, x)
		}
	}
	return out
}

// Category — категория команды по id или nil.
func (s *Store) Category(teamID, id string) *model.SidebarCategoryWithChannels {
	for _, c := range s.Categories(teamID) {
		if c.Id == id {
			return c
		}
	}
	return nil
}

// CategoryTeam — чьи категории раскладывают канал: его команда, у личек —
// первая.
func (s *Store) CategoryTeam(channelID string) string {
	ch := s.channels[channelID]
	if ch == nil {
		return ""
	}
	if !ch.IsGroupOrDirect() {
		return ch.TeamId
	}
	if len(s.teams) > 0 {
		return s.teams[0].Id
	}
	return ""
}

// CategoryOf — категория, где канал показан; nil — категорий нет.
func (s *Store) CategoryOf(channelID string) *model.SidebarCategoryWithChannels {
	team := s.CategoryTeam(channelID)
	for _, sec := range s.Sections() {
		if sec.Team == team && sec.Category != nil && slices.Contains(sec.Channels, channelID) {
			return sec.Category
		}
	}
	return nil
}

// CategoryTitle — название категории: встроенные сервер называет
// по-английски, веб-клиент переводит их по типу.
func CategoryTitle(c *model.SidebarCategoryWithChannels) string {
	switch c.Type {
	case model.SidebarCategoryFavorites:
		return "Избранное"
	case model.SidebarCategoryChannels:
		return "Каналы"
	case model.SidebarCategoryDirectMessages:
		return "Личные сообщения"
	}
	return c.DisplayName
}

// Sorting — как сортировать категорию: по умолчанию лички — по последней
// активности, остальное — по алфавиту.
func Sorting(c *model.SidebarCategoryWithChannels) model.SidebarCategorySorting {
	if c.Sorting != model.SidebarCategorySortDefault {
		return c.Sorting
	}
	if c.Type == model.SidebarCategoryDirectMessages {
		return model.SidebarCategorySortRecent
	}
	return model.SidebarCategorySortAlphabetical
}

// Accepts — может ли канал лежать в категории: в «Каналах» — не лички, в
// «Личных сообщениях» — только лички.
func Accepts(c *model.SidebarCategoryWithChannels, ch *model.Channel) bool {
	switch c.Type {
	case model.SidebarCategoryChannels:
		return !ch.IsGroupOrDirect()
	case model.SidebarCategoryDirectMessages:
		return ch.IsGroupOrDirect()
	}
	return true
}

// lastActivity — по чему сортировать «по последней активности».
func (s *Store) lastActivity(ch *model.Channel) int64 {
	if s.crt && ch.LastRootPostAt > 0 {
		return ch.LastRootPostAt
	}
	return max(ch.LastPostAt, ch.CreateAt)
}

func (s *Store) sortChannels(ids []string, how model.SidebarCategorySorting) {
	switch how {
	case model.SidebarCategorySortAlphabetical:
		slices.SortStableFunc(ids, func(a, b string) int {
			return cmp.Compare(strings.ToLower(s.Title(s.channels[a])), strings.ToLower(s.Title(s.channels[b])))
		})
	case model.SidebarCategorySortRecent:
		slices.SortStableFunc(ids, func(a, b string) int {
			return cmp.Compare(s.lastActivity(s.channels[b]), s.lastActivity(s.channels[a]))
		})
	}
}

// Sections — сайдбар по категориям: команды по порядку, у каждой — её
// категории; лички — по категориям первой команды, а при нескольких
// командах их категория — в самом конце. Каналы, которых нет ни в одной
// категории (только что вошли, а категории ещё старые), — в «Каналы» и
// «Личные сообщения». Пустое избранное не показываем. У команды без
// категорий — одна группа с её названием, лички — группой в конце.
func (s *Store) Sections() []Section {
	var out, tail []Section
	dmsPlaced := false
	for i, t := range s.teams {
		first := i == 0
		cats := s.Categories(t.Id)
		if cats == nil {
			var ids []string
			for _, ch := range s.TeamChannels(t.Id) {
				ids = append(ids, ch.Id)
			}
			if len(ids) > 0 {
				out = append(out, Section{Team: t.Id, Title: t.DisplayName, Channels: ids})
			}
			continue
		}
		placed := map[string]bool{}
		var secs []Section
		def := map[model.SidebarCategoryType]int{}
		for _, c := range cats {
			if c.Type == model.SidebarCategoryDirectMessages && !first {
				continue
			}
			sec := Section{Team: t.Id, Category: c, Title: CategoryTitle(c), Collapsed: c.Collapsed}
			for _, id := range c.Channels {
				ch := s.channels[id]
				if ch == nil || placed[id] || (ch.IsGroupOrDirect() && !first) || (!ch.IsGroupOrDirect() && ch.TeamId != t.Id) {
					continue
				}
				placed[id] = true
				sec.Channels = append(sec.Channels, id)
			}
			if _, ok := def[c.Type]; !ok {
				def[c.Type] = len(secs)
			}
			secs = append(secs, sec)
		}
		orphan := func(kind model.SidebarCategoryType, title string, chs []*model.Channel) {
			for _, ch := range chs {
				if placed[ch.Id] {
					continue
				}
				j, ok := def[kind]
				if !ok {
					def[kind] = len(secs)
					j = len(secs)
					secs = append(secs, Section{Team: t.Id, Title: title})
				}
				secs[j].Channels = append(secs[j].Channels, ch.Id)
			}
		}
		orphan(model.SidebarCategoryChannels, "Каналы", s.TeamChannels(t.Id))
		if first {
			orphan(model.SidebarCategoryDirectMessages, "Личные сообщения", s.DirectChannels())
			dmsPlaced = true
		}
		for _, sec := range secs {
			if sec.Category != nil {
				s.sortChannels(sec.Channels, Sorting(sec.Category))
			}
			if sec.Category != nil && sec.Category.Type == model.SidebarCategoryFavorites && len(sec.Channels) == 0 {
				continue
			}
			isDM := sec.Category != nil && sec.Category.Type == model.SidebarCategoryDirectMessages || sec.Category == nil && sec.Title == "Личные сообщения"
			if isDM && len(s.teams) > 1 {
				tail = append(tail, sec)
				continue
			}
			out = append(out, sec)
		}
	}
	if !dmsPlaced {
		var ids []string
		for _, ch := range s.DirectChannels() {
			ids = append(ids, ch.Id)
		}
		if len(ids) > 0 {
			tail = append(tail, Section{Title: "Личные сообщения", Channels: ids})
		}
	}
	return append(out, tail...)
}

// copyCategory — копия для отправки на сервер: запрос уходит из горутины,
// а свои категории главный поток может менять дальше.
func copyCategory(c *model.SidebarCategoryWithChannels) *model.SidebarCategoryWithChannels {
	cp := *c
	cp.Channels = slices.Clone(c.Channels)
	return &cp
}

// UpdateCategory — поменять категорию локально (свёрнута, сортировка,
// название); возвращает копию для сервера, nil — такой нет.
func (s *Store) UpdateCategory(teamID, id string, change func(c *model.SidebarCategoryWithChannels)) *model.SidebarCategoryWithChannels {
	c := s.Category(teamID, id)
	if c == nil {
		return nil
	}
	change(c)
	return copyCategory(c)
}

// MoveChannel — переложить канал в категорию to команды teamID. order —
// каналы категории в новом порядке показа (с перекладываемым); nil — канал
// в начало. Перестановка внутри категории делает её сортировку ручной, как
// в веб-клиенте. Возвращает копии изменённых категорий для сервера; nil —
// нельзя (нет категории, канал туда не кладут).
func (s *Store) MoveChannel(teamID, channelID, to string, order []string) []*model.SidebarCategoryWithChannels {
	dst, ch := s.Category(teamID, to), s.channels[channelID]
	if dst == nil || ch == nil || s.CategoryTeam(channelID) != teamID || !Accepts(dst, ch) {
		return nil
	}
	var src *model.SidebarCategoryWithChannels
	for _, c := range s.Categories(teamID) {
		if slices.Contains(c.Channels, channelID) {
			src = c
			break
		}
	}
	if order == nil {
		order = append([]string{channelID}, dst.Channels...)
	}
	shown := map[string]bool{}
	var next []string
	for _, id := range order {
		if !shown[id] {
			shown[id] = true
			next = append(next, id)
		}
	}
	if !shown[channelID] {
		next = append([]string{channelID}, next...)
		shown[channelID] = true
	}
	for _, id := range dst.Channels { // не показанные (лички в другой команде и т. п.) — в конец
		if !shown[id] {
			next = append(next, id)
		}
	}
	if src == dst {
		// Показан был порядок сортировки, а не хранимый: совпадение с
		// хранимым при сортировке не вручную ещё не значит «ничего не менялось».
		if slices.Equal(next, dst.Channels) && dst.Sorting == model.SidebarCategorySortManual {
			return nil
		}
		dst.Channels = next
		dst.Sorting = model.SidebarCategorySortManual
		return []*model.SidebarCategoryWithChannels{copyCategory(dst)}
	}
	dst.Channels = next
	out := []*model.SidebarCategoryWithChannels{copyCategory(dst)}
	if src != nil {
		src.Channels = slices.DeleteFunc(src.Channels, func(id string) bool { return id == channelID })
		out = append(out, copyCategory(src))
	}
	return out
}

// DefaultCategory — куда возвращается канал из избранного: «Каналы» или
// «Личные сообщения» его команды.
func (s *Store) DefaultCategory(channelID string) *model.SidebarCategoryWithChannels {
	ch := s.channels[channelID]
	if ch == nil {
		return nil
	}
	kind := model.SidebarCategoryChannels
	if ch.IsGroupOrDirect() {
		kind = model.SidebarCategoryDirectMessages
	}
	for _, c := range s.Categories(s.CategoryTeam(channelID)) {
		if c.Type == kind {
			return c
		}
	}
	return nil
}

// Favorite — в избранном ли канал.
func (s *Store) Favorite(channelID string) bool {
	c := s.CategoryOf(channelID)
	return c != nil && c.Type == model.SidebarCategoryFavorites
}

// FavoritesCategory — категория «Избранное» команды, где раскладывается канал.
func (s *Store) FavoritesCategory(channelID string) *model.SidebarCategoryWithChannels {
	for _, c := range s.Categories(s.CategoryTeam(channelID)) {
		if c.Type == model.SidebarCategoryFavorites {
			return c
		}
	}
	return nil
}

// MarkThreadsRead — все треды прочитаны (локально, как после ответа сервера).
func (s *Store) MarkThreadsRead() {
	for _, t := range s.threads {
		t.UnreadReplies, t.UnreadMentions = 0, 0
	}
}
