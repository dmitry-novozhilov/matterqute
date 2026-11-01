package store

import "github.com/mattermost/mattermost/server/public/model"

// NewPost — новый пост из WebSocket и то, что сервер сказал о нём именно нам.
type NewPost struct {
	Post        *model.Post
	Mentioned   bool   // мы в data.mentions (сервер учёл и ключевые слова)
	Following   bool   // мы в data.followers: подписаны на тред (CRT)
	ChannelType string // data.channel_type: канал может быть ещё не загружен
}

// ShouldNotify — показывать ли уведомление рабочего стола о посте; что
// канал уже открыт перед глазами, решает окно. Правила — как у веб-клиента
// (notification_actions.tsx): свои посты, системные, заглушённые каналы и
// «не беспокоить» молчат; уровень — настройка канала, иначе профиля, иначе
// «все»; в группе «по умолчанию» при «упоминаниях» в профиле — «все»; на
// «упоминаниях» — только упоминания и лички. Ответ при CRT — если упомянули
// или сервер включил нас в followers.
func (s *Store) ShouldNotify(e NewPost) bool {
	p := e.Post
	if (p.UserId == s.me.Id && p.GetProp(model.PostPropsFromWebhook) != "true") || p.IsSystemMessage() {
		return false
	}
	chType := model.ChannelType(e.ChannelType)
	if ch := s.channels[p.ChannelId]; ch != nil {
		chType = ch.Type
	}
	m := s.members[p.ChannelId]
	if m == nil && chType != model.ChannelTypeDirect {
		return false // канал ещё не знаем; новая личка — исключение: ей пишут первыми
	}
	if s.Muted(p.ChannelId) {
		return false
	}
	if st := s.Status(s.me.Id); st == model.StatusDnd || st == model.StatusOutOfOffice {
		return false
	}

	level := ""
	if m != nil {
		level = m.NotifyProps[model.DesktopNotifyProp]
	}
	userLevel := s.me.NotifyProps[model.DesktopNotifyProp]
	if level == "" || level == model.ChannelNotifyDefault {
		level = userLevel
		if chType == model.ChannelTypeGroup && userLevel == model.UserNotifyMention {
			level = model.UserNotifyAll
		}
	}
	if level == "" {
		level = model.UserNotifyAll
	}
	switch {
	case level == model.UserNotifyNone:
		return false
	case s.crt && p.RootId != "":
		// В followers сервер кладёт только тех подписчиков треда, кому по их
		// desktop и desktop_threads положено уведомление (notification.go,
		// addFollowerToNotify), — уровень канала здесь уже учтён.
		return e.Following || e.Mentioned
	case level == model.UserNotifyMention:
		return e.Mentioned || chType == model.ChannelTypeDirect
	}
	return true
}
