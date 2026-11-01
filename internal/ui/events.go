package ui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
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
		mentioned, err := eventMentions(data, me)
		if err != nil {
			return nil, err
		}
		return func() { m.applyPosted(p, mentioned) }, nil

	case model.WebsocketEventPostEdited:
		p, err := eventPost(data)
		if err != nil {
			return nil, err
		}
		return func() {
			if m.st.ApplyEdited(p) && p.ChannelId == m.current && m.feed.Has(p.Id) {
				m.feed.Upsert(p)
			}
			if t := m.thread; t != nil && t.tl.Replace(p) && t.tl.Loaded {
				m.panel.feed.Upsert(p)
			}
		}, nil

	case model.WebsocketEventPostDeleted:
		p, err := eventPost(data)
		if err != nil {
			return nil, err
		}
		return func() {
			if m.st.ApplyDeleted(p) && p.ChannelId == m.current {
				m.feed.Remove(p.Id)
			}
			if t := m.thread; t != nil {
				if p.Id == t.rootID {
					m.closeThread()
				} else if t.tl.Remove(p.Id) {
					m.panel.feed.Remove(p.Id)
				}
			}
		}, nil

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

	case model.WebsocketEventPreferencesChanged:
		// Пользователь мог включить или выключить свёрнутые треды.
		if raw, _ := data["preferences"].(string); strings.Contains(raw, model.PreferenceNameCollapsedThreadsEnabled) {
			return m.reloadSidebar, nil
		}

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
		return func() {
			m.st.AddUsers([]*model.User{&u})
			m.feed.RefreshNames()
			for id := range m.items {
				m.refreshItem(id)
			}
		}, nil

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

// eventMentions — упомянут ли me. Поле mentions сервер добавляет только
// упомянутым, JSON-строкой с массивом id (web_broadcast_hooks.go).
func eventMentions(data map[string]any, me string) (bool, error) {
	raw, ok := data["mentions"]
	if !ok {
		return false, nil
	}
	s, ok := raw.(string)
	if !ok {
		return false, fmt.Errorf("mentions: want JSON string, got %T", raw)
	}
	var ids []string
	if err := json.Unmarshal([]byte(s), &ids); err != nil {
		return false, fmt.Errorf("mentions %s: %w", truncate(s, 200), err)
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
