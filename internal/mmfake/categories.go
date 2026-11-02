package mmfake

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/mattermost/mattermost/server/public/model"
)

// Категории сайдбара, как у сервера: у пользователя в каждой команде —
// избранное, каналы, личные сообщения и свои; канал — ровно в одной.
// Новые членства раскладываются при чтении: каналы — в начало «Каналов»,
// лички — в «Личные сообщения». Изменения — событиями sidebar_category_*.
// Ещё здесь «прочитать каналы разом» и «прочитать все треды».

// userCategories — категории пользователя в команде.
type userCategories struct {
	cats  map[string]*model.SidebarCategoryWithChannels
	order []string
}

// categoriesFor — категории пользователя в команде, разложенные по
// нынешним членствам (под s.mu).
func (s *Server) categoriesFor(uid, tid string) *userCategories {
	key := uid + "/" + tid
	uc := s.categories[key]
	if uc == nil {
		uc = &userCategories{cats: map[string]*model.SidebarCategoryWithChannels{}}
		for _, c := range []struct {
			kind model.SidebarCategoryType
			name string
		}{{model.SidebarCategoryFavorites, "Favorites"}, {model.SidebarCategoryChannels, "Channels"}, {model.SidebarCategoryDirectMessages, "Direct Messages"}} {
			id := string(c.kind) + "_" + uid + "_" + tid
			uc.cats[id] = &model.SidebarCategoryWithChannels{
				SidebarCategory: model.SidebarCategory{Id: id, UserId: uid, TeamId: tid, Type: c.kind, DisplayName: c.name},
				Channels:        []string{},
			}
			uc.order = append(uc.order, id)
		}
		s.categories[key] = uc
	}
	placed := map[string]bool{}
	for _, c := range uc.cats {
		c.Channels = slices.DeleteFunc(c.Channels, func(id string) bool {
			ch := s.channels[id]
			return ch == nil || s.members[uid][id] == nil || !ch.IsGroupOrDirect() && ch.TeamId != tid
		})
		for _, id := range c.Channels {
			placed[id] = true
		}
	}
	var chans, dms []string
	for id := range s.members[uid] {
		ch := s.channels[id]
		switch {
		case ch == nil || placed[id]:
		case ch.IsGroupOrDirect():
			dms = append(dms, id)
		case ch.TeamId == tid:
			chans = append(chans, id)
		}
	}
	slices.Sort(chans)
	slices.Sort(dms)
	for _, c := range uc.cats {
		switch c.Type {
		case model.SidebarCategoryChannels:
			c.Channels = append(chans, c.Channels...)
		case model.SidebarCategoryDirectMessages:
			c.Channels = append(dms, c.Channels...)
		}
	}
	return uc
}

func (uc *userCategories) ordered() *model.OrderedSidebarCategories {
	out := &model.OrderedSidebarCategories{Order: slices.Clone(uc.order)}
	for _, id := range uc.order {
		out.Categories = append(out.Categories, uc.cats[id])
	}
	return out
}

// categoryEvent — пользователю о его категориях в команде.
func (s *Server) categoryEvent(kind model.WebsocketEventType, uid, tid, key, value string) {
	ev := model.NewWebSocketEvent(kind, tid, "", uid, nil, "")
	ev.Add(key, value)
	s.toUser(uid, ev)
}

// categories — GET /users/{uid}/teams/{tid}/channels/categories.
func (s *Server) listCategories(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.PathValue("uid") != me.Id {
		s.fail(w, http.StatusForbidden, "api.context.permissions.app_error")
		return
	}
	s.json(w, s.categoriesFor(me.Id, r.PathValue("tid")).ordered())
}

// take — каналы ids уходят из остальных категорий в c.
func (uc *userCategories) take(c *model.SidebarCategoryWithChannels, ids []string) {
	for _, o := range uc.cats {
		if o != c {
			o.Channels = slices.DeleteFunc(o.Channels, func(id string) bool { return slices.Contains(ids, id) })
		}
	}
}

// createCategory — POST: своя категория, сразу после избранного.
func (s *Server) createCategory(w http.ResponseWriter, r *http.Request, me *model.User) {
	var c model.SidebarCategoryWithChannels
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil || c.DisplayName == "" {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tid := r.PathValue("tid")
	uc := s.categoriesFor(me.Id, tid)
	c.Id, c.UserId, c.TeamId, c.Type = "custom_"+model.NewId(), me.Id, tid, model.SidebarCategoryCustom
	if c.Channels == nil {
		c.Channels = []string{}
	}
	uc.take(&c, c.Channels)
	uc.cats[c.Id] = &c
	uc.order = slices.Insert(uc.order, 1, c.Id)
	s.categoryEvent(model.WebsocketEventSidebarCategoryCreated, me.Id, tid, "category_id", c.Id)
	w.WriteHeader(http.StatusCreated)
	s.json(w, &c)
}

// updateCategories — PUT (список) или PUT /{cid} (одна): название (у
// своих), сортировка, свёрнута, каналы.
func (s *Server) updateCategories(w http.ResponseWriter, r *http.Request, me *model.User) {
	var in []*model.SidebarCategoryWithChannels
	if cid := r.PathValue("cid"); cid != "" {
		var one model.SidebarCategoryWithChannels
		if err := json.NewDecoder(r.Body).Decode(&one); err != nil || one.Id != cid {
			s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
			return
		}
		in = append(in, &one)
	} else if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tid := r.PathValue("tid")
	uc := s.categoriesFor(me.Id, tid)
	var out []*model.SidebarCategoryWithChannels
	for _, c := range in {
		have := uc.cats[c.Id]
		if have == nil {
			s.fail(w, http.StatusNotFound, "app.channel.sidebar_categories.app_error")
			return
		}
		for _, id := range c.Channels {
			ch := s.channels[id]
			if ch == nil || have.Type == model.SidebarCategoryChannels && ch.IsGroupOrDirect() ||
				have.Type == model.SidebarCategoryDirectMessages && !ch.IsGroupOrDirect() {
				s.fail(w, http.StatusBadRequest, "app.channel.sidebar_categories.app_error")
				return
			}
		}
		if have.Type == model.SidebarCategoryCustom && c.DisplayName != "" {
			have.DisplayName = c.DisplayName
		}
		have.Sorting, have.Collapsed, have.Muted = c.Sorting, c.Collapsed, c.Muted
		have.Channels = slices.Clone(c.Channels)
		if have.Channels == nil {
			have.Channels = []string{}
		}
		uc.take(have, have.Channels)
		out = append(out, have)
	}
	data, _ := json.Marshal(out)
	s.categoryEvent(model.WebsocketEventSidebarCategoryUpdated, me.Id, tid, "updatedCategories", string(data))
	if r.PathValue("cid") != "" {
		s.json(w, out[0])
		return
	}
	s.json(w, out)
}

// deleteCategory — DELETE /{cid}: только свои; каналы — обратно в
// «Каналы» и «Личные сообщения».
func (s *Server) deleteCategory(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tid, cid := r.PathValue("tid"), r.PathValue("cid")
	uc := s.categoriesFor(me.Id, tid)
	c := uc.cats[cid]
	if c == nil || c.Type != model.SidebarCategoryCustom {
		s.fail(w, http.StatusBadRequest, "api.channel.delete_category.not_custom.app_error")
		return
	}
	delete(uc.cats, cid)
	uc.order = slices.DeleteFunc(uc.order, func(id string) bool { return id == cid })
	s.categoriesFor(me.Id, tid) // каналы удалённой — по местам
	s.categoryEvent(model.WebsocketEventSidebarCategoryDeleted, me.Id, tid, "category_id", cid)
	s.json(w, map[string]string{"status": "OK"})
}

// markRead — POST /channels/members/{uid}/mark_read: прочитать каналы разом.
func (s *Server) markRead(w http.ResponseWriter, r *http.Request, me *model.User) {
	var ids []string
	if err := json.NewDecoder(r.Body).Decode(&ids); err != nil || r.PathValue("uid") != me.Id {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	times := map[string]int64{}
	for _, id := range ids {
		if m, c := s.members[me.Id][id], s.channels[id]; m != nil && c != nil {
			m.MsgCount, m.MsgCountRoot, m.MentionCount, m.MentionCountRoot = c.TotalMsgCount, c.TotalMsgCountRoot, 0, 0
			m.LastViewedAt = model.GetMillis()
			times[id] = m.LastViewedAt
		}
	}
	s.json(w, &model.ChannelViewResponse{Status: "OK", LastViewedAtTimes: times})
}

// threadsRead — PUT /users/{uid}/teams/{tid}/threads/read: все треды прочитаны.
func (s *Server) threadsRead(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.followers {
		if m := f[me.Id]; m != nil {
			m.unreadReplies, m.unreadMentions = 0, 0
		}
	}
	s.json(w, map[string]string{"status": "OK"})
}
