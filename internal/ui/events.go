package ui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/store"
)

// decodeEvent разбирает событие в горутине WebSocket и возвращает, что сделать
// в главном потоке; nil — событие нам не нужно.
func (m *MainWindow) decodeEvent(ev *model.WebSocketEvent) (func(), error) {
	data := ev.GetData()
	me := m.s.Me.Id
	switch ev.EventType() {
	case model.WebsocketEventPosted:
		p, err := eventPost(data)
		if err != nil {
			return nil, err
		}
		mentioned, err := eventUserList(data, "mentions", me)
		if err != nil {
			return nil, err
		}
		following, err := eventUserList(data, "followers", me)
		if err != nil {
			return nil, err
		}
		chType, _ := data["channel_type"].(string)
		return func() {
			m.applyPosted(store.NewPost{Post: p, Mentioned: mentioned, Following: following, ChannelType: chType})
		}, nil

	case model.WebsocketEventPostEdited:
		p, err := eventPost(data)
		if err != nil {
			return nil, err
		}
		return func() { m.applyEdited(p) }, nil

	case model.WebsocketScheduledPostCreated, model.WebsocketScheduledPostUpdated, model.WebsocketScheduledPostDeleted:
		sp, err := eventScheduledPost(data)
		if err != nil {
			return nil, err
		}
		kind := ev.EventType()
		return func() { m.sched.applyEvent(kind, sp) }, nil

	case model.WebsocketEventTyping:
		user, _ := data["user_id"].(string)
		parent, _ := data["parent_id"].(string)
		ch := ev.GetBroadcast().ChannelId
		return func() { m.applyTyping(ch, parent, user) }, nil

	case model.WebsocketEventEphemeralMessage:
		p, err := eventPost(data)
		if err != nil {
			return nil, err
		}
		return func() { m.applyEphemeral(p) }, nil

	case model.WebsocketEventPostDeleted:
		p, err := eventPost(data)
		if err != nil {
			return nil, err
		}
		return func() { m.applyDeleted(p) }, nil

	case model.WebsocketEventReactionAdded, model.WebsocketEventReactionRemoved:
		raw, ok := data["reaction"].(string)
		if !ok {
			return nil, fmt.Errorf("reaction: want JSON string, got %T", data["reaction"])
		}
		var r model.Reaction
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, fmt.Errorf("reaction %s: %w", truncate(raw, 200), err)
		}
		added := ev.EventType() == model.WebsocketEventReactionAdded
		channel := ev.GetBroadcast().ChannelId
		return func() { m.applyReaction(channel, &r, added) }, nil

	case model.WebsocketEventThreadUpdated:
		raw, ok := data["thread"].(string)
		if !ok {
			return nil, fmt.Errorf("thread: want JSON string, got %T", data["thread"])
		}
		var t model.ThreadResponse
		if err := json.Unmarshal([]byte(raw), &t); err != nil {
			return nil, fmt.Errorf("thread %s: %w", truncate(raw, 200), err)
		}
		return func() {
			m.st.ApplyThread(&t)
			m.withUsers(threadPostAuthors([]*model.ThreadResponse{&t}), m.refreshThreads)
		}, nil

	case model.WebsocketEventThreadReadChanged:
		id, _ := data["thread_id"].(string)
		if id == "" {
			// Все треды команды отмечены прочитанными в другом клиенте.
			return m.reloadSidebar, nil
		}
		replies, _ := data["unread_replies"].(float64)
		mentions, _ := data["unread_mentions"].(float64)
		return func() {
			if m.st.ApplyThreadRead(id, int64(replies), int64(mentions)) {
				m.refreshThreads()
			}
		}, nil

	case model.WebsocketEventThreadFollowChanged:
		return m.reloadSidebar, nil

	case model.WebsocketEventPreferencesChanged, model.WebsocketEventPreferencesDeleted:
		saved, err := savedPrefs(data)
		if err != nil {
			return nil, err
		}
		changed := ev.EventType() == model.WebsocketEventPreferencesChanged
		// Пользователь мог включить или выключить свёрнутые треды или предпросмотр ссылок.
		raw, _ := data["preferences"].(string)
		reload := changed && (strings.Contains(raw, model.PreferenceNameCollapsedThreadsEnabled) || strings.Contains(raw, `"link_previews"`))
		if len(saved) == 0 && !reload {
			return nil, nil
		}
		return func() {
			for _, p := range saved {
				m.applySaved(p.Name, changed && p.Value == "true")
			}
			if reload {
				m.reloadSidebar()
			}
		}, nil

	case model.WebsocketEventDraftCreated, model.WebsocketEventDraftUpdated, model.WebsocketEventDraftDeleted:
		dr, err := eventDraft(data)
		if err != nil {
			return nil, err
		}
		deleted := ev.EventType() == model.WebsocketEventDraftDeleted
		return func() { m.drafts.remote(dr, deleted) }, nil

	case model.WebsocketEventPostUnread:
		// Пометили непрочитанным (здесь или в другом клиенте): счётчики — с сервера.
		ch := ev.GetBroadcast().ChannelId
		if ch == "" {
			return nil, fmt.Errorf("post_unread without channel: %v", data)
		}
		return func() { m.refreshMember(ch) }, nil

	case model.WebsocketEventMultipleChannelsViewed:
		// Прочитано в другом клиенте.
		times, ok := data["channel_times"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("channel_times: want object, got %T", data["channel_times"])
		}
		return func() {
			for id := range times {
				m.st.MarkViewed(id)
				m.refreshItem(id)
			}
		}, nil

	case model.WebsocketEventStatusChange:
		userID, _ := data["user_id"].(string)
		status, _ := data["status"].(string)
		if userID == "" || status == "" {
			return nil, fmt.Errorf("status_change: user_id %q, status %q", userID, status)
		}
		return func() {
			m.st.SetStatus(userID, status)
			m.refreshItem("")
		}, nil

	case model.WebsocketEventUserUpdated:
		var u model.User
		if err := remarshal(data["user"], &u); err != nil {
			return nil, fmt.Errorf("user: %w", err)
		}
		return func() { m.applyUser(&u) }, nil

	case model.WebsocketEventUserAdded, model.WebsocketEventUserRemoved:
		// Шлётся всем участникам канала; сайдбар меняется, только если это мы.
		if data["user_id"] == me || ev.GetBroadcast().UserId == me {
			return m.reloadSidebar, nil
		}

	case model.WebsocketEventChannelCreated, model.WebsocketEventChannelDeleted,
		model.WebsocketEventChannelRestored, model.WebsocketEventChannelUpdated,
		model.WebsocketEventChannelConverted, model.WebsocketEventChannelMemberUpdated,
		model.WebsocketEventDirectAdded, model.WebsocketEventGroupAdded,
		model.WebsocketEventAddedToTeam, model.WebsocketEventLeaveTeam:
		return m.reloadSidebar, nil
	}
	return nil, nil
}

// eventPost — пост из data["post"]: сервер кладёт его JSON-строкой.
func eventPost(data map[string]any) (*model.Post, error) {
	raw, ok := data["post"].(string)
	if !ok {
		return nil, fmt.Errorf("post: want JSON string, got %T", data["post"])
	}
	var p model.Post
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, fmt.Errorf("post %s: %w", truncate(raw, 200), err)
	}
	return &p, nil
}

// eventUserList — есть ли me в списке id из события: mentions (упомянутые;
// сервер добавляет поле только им, web_broadcast_hooks.go) или followers
// (подписчики треда при CRT). Список — JSON-строка с массивом.
func eventUserList(data map[string]any, key, me string) (bool, error) {
	raw, ok := data[key]
	if !ok {
		return false, nil
	}
	s, ok := raw.(string)
	if !ok {
		return false, fmt.Errorf("%s: want JSON string, got %T", key, raw)
	}
	var ids []string
	if err := json.Unmarshal([]byte(s), &ids); err != nil {
		return false, fmt.Errorf("%s %s: %w", key, truncate(s, 200), err)
	}
	return slices.Contains(ids, me), nil
}

func remarshal(v any, out any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("%s: %w", truncate(string(b), 200), err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// applyEdited — пост изменили (здесь или в другом клиенте).
func (m *MainWindow) applyEdited(p *model.Post) {
	if m.st.ApplyEdited(p) {
		if f := m.feedWith(p.ChannelId, p.Id); f != nil {
			f.Upsert(p)
		}
	}
	if t := m.thread; t != nil && t.tl.Replace(p) && t.tl.Loaded {
		m.panel.feed.Upsert(p)
	}
	if m.listShown(listPinned) && m.search.listChannel == p.ChannelId && (p.IsPinned || m.search.feed.Has(p.Id)) {
		m.showList(listPinned) // закрепили или открепили
	} else if m.search.feed.Has(p.Id) {
		m.search.feed.Upsert(p)
	}
}
