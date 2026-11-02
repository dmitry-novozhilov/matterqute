package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/store"
)

// Действия с сообщением (меню ленты): закрепить в канале и открепить,
// сохранить и убрать из сохранённых (настройка flagged_post — общая с
// веб-клиентом), пометить непрочитанным. Списки закреплённого в открытом
// канале и сохранённого — в правой колонке, как результаты поиска.
//
// «Непрочитанное» держится, пока пользователь в канале или треде: ни возврат
// в окно, ни новые сообщения не отмечают их прочитанными (manualUnread), и
// уход из канала тоже (prev в ViewChannel не передаём) — как веб-клиент.
// Снимается, когда в канал или тред вернутся.

const (
	listPinned   = "pinned"
	listSaved    = "saved"
	listMentions = "mentions" // сообщения, где меня упомянули (как «Recent mentions» веб-клиента)
)

// mentionTerms — запрос «где меня упомянули», как у веб-клиента: @имя,
// имя (если оно в настройках уведомлений) и свои слова-упоминания; ищется
// любое из них (без @channel, @here, @all).
func mentionTerms(me *model.User) string {
	keys := []string{"@" + me.Username}
	seen := map[string]bool{strings.ToLower(me.Username): true, "@" + strings.ToLower(me.Username): true,
		"@channel": true, "@all": true, "@here": true, "channel": true, "all": true, "here": true}
	add := func(k string) {
		if k = strings.TrimSpace(k); k != "" && !seen[strings.ToLower(k)] {
			seen[strings.ToLower(k)] = true
			keys = append(keys, k)
		}
	}
	if me.NotifyProps[model.FirstNameNotifyProp] == "true" {
		add(me.FirstName)
	}
	for _, k := range strings.Split(me.NotifyProps[model.MentionKeysNotifyProp], ",") {
		add(k)
	}
	return strings.Join(keys, " ")
}

func (m *MainWindow) pinPost(p *model.Post, on bool) {
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		var err error
		if on {
			_, err = m.s.Client.PinPost(ctx, p.Id)
		} else {
			_, err = m.s.Client.UnpinPost(ctx, p.Id)
		}
		m.ui(func() {
			if err != nil {
				m.fail("pin post", fmt.Errorf("pin=%v post %s: %w", on, p.Id, err))
				return
			}
			// То же придёт событием post_edited; повтор ничего не изменит.
			q := p.Clone()
			q.IsPinned = on
			m.applyEdited(q)
		})
	}()
}

func (m *MainWindow) savePost(p *model.Post, on bool) {
	prefs := model.Preferences{{UserId: m.s.Me.Id, Category: model.PreferenceCategoryFlaggedPost, Name: p.Id, Value: "true"}}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		var err error
		if on {
			_, err = m.s.Client.UpdatePreferences(ctx, m.s.Me.Id, prefs)
		} else {
			_, err = m.s.Client.DeletePreferences(ctx, m.s.Me.Id, prefs)
		}
		m.ui(func() {
			if err != nil {
				m.fail("save post", fmt.Errorf("save=%v post %s: %w", on, p.Id, err))
				return
			}
			m.applySaved(p.Id, on)
		})
	}()
}

// applySaved — пост сохранили или убрали из сохранённых (здесь или в другом клиенте).
func (m *MainWindow) applySaved(id string, on bool) {
	if m.st.Saved(id) == on {
		return
	}
	m.st.SetSaved(id, on)
	m.refreshPost(id)
	if m.listShown(listSaved) {
		m.showList(listSaved)
	}
}

// refreshPost — перерисовать пост во всех лентах, где он есть.
func (m *MainWindow) refreshPost(id string) {
	match := func(p *model.Post) bool { return p.Id == id }
	m.eachFeed(func(f *Feed) {
		if f.Has(id) {
			f.invalidate(match)
		}
	})
	for _, f := range []*Feed{m.panel.feed, m.search.feed} {
		if f.Has(id) {
			f.invalidate(match)
		}
	}
}

// markUnread — «Пометить непрочитанным»: канал с этого поста, а ответ в
// панели треда при свёрнутых тредах — тред.
func (m *MainWindow) markUnread(f *Feed, p *model.Post) {
	if f == m.panel.feed && m.st.CRT() && p.RootId != "" {
		m.markThreadUnread(p)
		return
	}
	crt := m.st.CRT()
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		_, err := m.s.Client.SetPostUnread(ctx, m.s.Me.Id, p.Id, crt)
		var member *model.ChannelMember
		if err == nil {
			// Ответ счётчиков не отдаёт (их шлёт событие post_unread) — берём участие.
			member, _, err = m.s.Client.GetChannelMember(ctx, p.ChannelId, m.s.Me.Id, "")
		}
		m.ui(func() {
			if err != nil {
				m.fail("mark unread", fmt.Errorf("mark channel %s unread from post %s: %w", p.ChannelId, p.Id, err))
				return
			}
			m.applyMember(member)
		})
	}()
}

func (m *MainWindow) markThreadUnread(p *model.Post) {
	team := m.st.TeamFor(p.ChannelId)
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		th, _, err := m.s.Client.SetThreadUnreadByPostId(ctx, m.s.Me.Id, team, p.RootId, p.Id)
		m.ui(func() {
			if err != nil {
				m.fail("mark thread unread", fmt.Errorf("mark thread %s unread from post %s: %w", p.RootId, p.Id, err))
				return
			}
			m.manualUnread[p.RootId] = true
			m.st.ApplyThread(th)
			m.refreshThreads()
		})
	}()
}

// refreshMember — участие в канале изменилось на сервере (post_unread:
// пометили непрочитанным, может быть, в другом клиенте).
func (m *MainWindow) refreshMember(channelID string) {
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		member, _, err := m.s.Client.GetChannelMember(ctx, channelID, m.s.Me.Id, "")
		m.ui(func() {
			if err != nil {
				m.log.Warn("load channel membership", "channel", channelID, "err", err)
				return
			}
			m.applyMember(member)
		})
	}()
}

// applyMember — счётчики канала с сервера: сайдбар, а открытый канал ещё и
// остаётся непрочитанным и показывает «Новые сообщения» с того места.
func (m *MainWindow) applyMember(member *model.ChannelMember) {
	m.st.SetMember(member)
	if unread, _ := m.st.Unread(member.ChannelId); unread && member.ChannelId == m.current {
		m.manualUnread[member.ChannelId] = true
		m.feed.SetNewSince(member.LastViewedAt)
	}
	m.refreshItem(member.ChannelId)
}

// listShown — в правой колонке список kind.
func (m *MainWindow) listShown(kind string) bool {
	return m.search.w.IsVisible() && m.search.list == kind
}

// toggleList — кнопка в шапке: список kind, а если он уже открыт — закрыть.
func (m *MainWindow) toggleList(kind string) {
	if m.listShown(kind) && (kind != listPinned || m.search.listChannel == m.current) {
		m.closeSearch()
		return
	}
	m.showList(kind)
}

// showList — закреплённые в открытом канале или сохранённые: в правой колонке
// вместо поиска, тем же видом (где · когда · Перейти), новые сверху.
func (m *MainWindow) showList(kind string) {
	sp := m.search
	if m.thread != nil {
		m.closeThread()
	}
	channel := m.current
	if kind == listPinned && m.st.Channel(channel) == nil {
		return
	}
	sp.setList(kind)
	sp.listChannel = channel
	sp.gen++
	gen := sp.gen
	sub := ""
	if kind == listPinned {
		sub = channelPrefix(m.st.Channel(channel)) + m.st.Title(m.st.Channel(channel))
	}
	mentions, team := mentionTerms(m.me()), m.teamOf(m.current)
	if kind == listMentions {
		sub = "Где упомянули вас"
	}
	sp.head.set(iconPixmap(sp.iconName(), Pal().Tx2, 16), sp.title(), sub)
	sp.feed.SetMarks(nil)
	if kind == listMentions {
		sp.feed.SetMarks(searchTerms(strings.ReplaceAll(mentions, "@", ""))) // свои слова-упоминания — подсветкой
	}
	sp.feed.Loading()
	sp.info.SetText("")
	m.showRight(sp.w)
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		var list *model.PostList
		var err error
		switch kind {
		case listPinned:
			list, _, err = m.s.Client.GetPinnedPosts(ctx, channel, "")
		case listMentions:
			_, offset := time.Now().Zone()
			params := &model.SearchParameter{Terms: &mentions, IsOrSearch: new(true), TimeZoneOffset: &offset, Page: new(0), PerPage: new(searchLimit)}
			list, _, err = m.s.Client.SearchPostsWithParams(ctx, "", params) // по всем командам
			if err != nil && team != "" {
				list, _, err = m.s.Client.SearchPostsWithParams(ctx, team, params) // сервер постарше — по команде
			}
		default:
			list, _, err = m.s.Client.GetFlaggedPostsForUser(ctx, m.s.Me.Id, 0, searchLimit)
		}
		m.ui(func() {
			if sp.gen != gen {
				return
			}
			if err != nil {
				m.log.Warn("load list", "list", kind, "channel", channel, "err", err)
				sp.feed.Reset("")
				sp.info.SetText("Не загрузилось: " + commandError(err))
				return
			}
			posts := store.PostsOf(list)
			slices.SortFunc(posts, func(a, b *model.Post) int { return int(b.CreateAt - a.CreateAt) })
			m.withUsers(postAuthors(posts), func() {
				if sp.gen != gen {
					return
				}
				if len(posts) == 0 {
					switch kind {
					case listPinned:
						sp.feed.Reset("В канале ничего не закреплено")
						sp.info.SetText("Закрепить — «Закрепить в канале» в меню сообщения.")
					case listMentions:
						sp.feed.Reset("Вас пока не упоминали")
						sp.info.SetText(html.EscapeString("Ищем: " + mentions))
					default:
						sp.feed.Reset("Сохранённых сообщений нет")
						sp.info.SetText("Сохранить — «Сохранить» в меню сообщения.")
					}
					return
				}
				sp.feed.SetPosts(posts, false)
				sp.feed.ScrollToTop()
				sp.info.SetText(pluralMessages(len(posts)))
			})
		})
	}()
}

// pluralMessages — «1 сообщение», «3 сообщения», «5 сообщений».
func pluralMessages(n int) string {
	switch {
	case n%10 == 1 && n%100 != 11:
		return fmt.Sprintf("%d сообщение", n)
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return fmt.Sprintf("%d сообщения", n)
	}
	return fmt.Sprintf("%d сообщений", n)
}

// savedPrefs — сохранённые посты из событий preferences_changed/deleted.
func savedPrefs(data map[string]any) (model.Preferences, error) {
	raw, _ := data["preferences"].(string)
	if raw == "" {
		return nil, nil
	}
	var prefs model.Preferences
	if err := json.Unmarshal([]byte(raw), &prefs); err != nil {
		return nil, fmt.Errorf("preferences %s: %w", truncate(raw, 200), err)
	}
	return slices.DeleteFunc(prefs, func(p model.Preference) bool { return p.Category != model.PreferenceCategoryFlaggedPost }), nil
}

// savedIDs — сохранённые посты пользователя для сайдбара; nil — не вышло
// (тогда прежние остаются). Нет ни одного — сервер мог ответить 404.
func (m *MainWindow) savedIDs(ctx context.Context) []string {
	prefs, resp, err := m.s.Client.GetPreferencesByCategory(ctx, m.s.Me.Id, model.PreferenceCategoryFlaggedPost)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return []string{}
		}
		m.log.Warn("load saved posts", "err", err)
		return nil
	}
	ids := []string{}
	for _, p := range prefs {
		if p.Value == "true" {
			ids = append(ids, p.Name)
		}
	}
	return ids
}
