package ui

import (
	"fmt"
	"os"
	"strings"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/markdown"
	"matterqute/internal/notify"
	"matterqute/internal/store"
)

// Notifier показывает уведомления рабочего стола (notify.Notifier; в тестах — подделка).
type Notifier interface {
	Show(notify.Notification) (uint32, error)
}

// maybeNotify — уведомление о новом посте, если правила (store.ShouldNotify)
// велят и пользователь не смотрит прямо на него: окно активно, а открыт этот
// канал или, для ответа при CRT, этот тред.
func (m *MainWindow) maybeNotify(e store.NewPost) {
	if m.notifier == nil || !m.st.ShouldNotify(e) {
		return
	}
	p := e.Post
	if m.w.IsActiveWindow() {
		if m.st.CRT() && p.RootId != "" {
			if t := m.thread; t != nil && t.rootID == p.RootId {
				return
			}
		} else if p.ChannelId == m.current {
			return
		}
	}
	m.withUsers([]string{p.UserId}, func() {
		key := p.ChannelId
		if p.RootId != "" {
			key = "thread:" + p.RootId
		}
		n := m.notification(p)
		n.Replaces = m.notes[key] // пачка постов в одном месте — одно уведомление, последнее
		n.OnOpen = func(token string) {
			m.ui(func() {
				delete(m.notes, key)
				m.openFromNotification(p, token)
			})
		}
		id, err := m.notifier.Show(n)
		if err != nil {
			m.log.Warn("show notification", "channel", p.ChannelId, "post", p.Id, "err", err)
			return
		}
		m.notes[key] = id
		qt.QApplication_Alert(m.w) // значок окна на панели задач просит внимания
	})
}

// notification — «bob в #backend» и текст без разметки, по дизайну.
func (m *MainWindow) notification(p *model.Post) notify.Notification {
	in, hook := store.PostIntegration(p)
	author := m.st.Username(p.UserId)
	if hook && in.Name != "" {
		author = in.Name
	}
	where := ""
	if ch := m.st.Channel(p.ChannelId); ch != nil {
		switch ch.Type {
		case model.ChannelTypeOpen, model.ChannelTypePrivate:
			where = "#" + m.st.Title(ch)
		case model.ChannelTypeGroup:
			where = m.st.Title(ch)
		}
	}
	title := author
	switch {
	case p.RootId != "" && where != "":
		title += " в треде, " + where
	case p.RootId != "":
		title += " в треде"
	case where != "":
		title += " в " + where
	}

	image := ""
	if m.assets != nil {
		if hook && !in.CreatorIcon && in.IconURL != "" {
			image = m.assets.Icon(in.IconURL)
		} else if !hook || in.CreatorIcon {
			image = m.assets.Avatar(m.st.User(p.UserId))
		}
	}
	return notify.Notification{Title: title, Body: notificationBody(p), Image: image}
}

// notificationBody — текст поста без разметки; у пустого — текст вложения
// интеграции или имена файлов.
func notificationBody(p *model.Post) string {
	text := markdown.PlainText(p.Message)
	if text == "" {
		for _, a := range p.Attachments() {
			for _, s := range []string{a.Fallback, a.Pretext, a.Title, a.Text} {
				if text = markdown.PlainText(s); text != "" {
					break
				}
			}
			if text != "" {
				break
			}
		}
	}
	if text == "" && p.Metadata != nil && len(p.Metadata.Files) > 0 {
		var names []string
		for _, fi := range p.Metadata.Files {
			names = append(names, fi.Name)
		}
		text = "📎 " + strings.Join(names, ", ")
		if len(names) > 3 {
			text = fmt.Sprintf("📎 %s и ещё %d", strings.Join(names[:2], ", "), len(names)-2)
		}
	}
	if r := []rune(text); len(r) > 300 {
		text = string(r[:300]) + "…"
	}
	return text
}

// openFromNotification — клик по уведомлению: окно наверх, канал поста, а
// для ответа — и его тред.
func (m *MainWindow) openFromNotification(p *model.Post, token string) {
	if token != "" {
		// Wayland отдаёт фокус только по токену активации; Qt берёт его из
		// окружения при активации окна и сам стирает.
		os.Setenv("XDG_ACTIVATION_TOKEN", token)
	}
	if m.OnRaise != nil {
		m.OnRaise()
	}
	if m.items[p.ChannelId] != nil {
		m.selectChannel(p.ChannelId)
	} else {
		m.selectAfterLoad = p.ChannelId // новая личка: сайдбар ещё перезагружается
	}
	if p.RootId != "" {
		m.openThread(p.RootId)
	}
}
