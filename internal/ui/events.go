package ui

import (
	"encoding/json"
	"fmt"
	"slices"

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
			if m.st.ApplyEdited(p) && p.ChannelId == m.current {
				m.feed.Upsert(p)
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
		}, nil

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
