package store

import (
	"net/url"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// Integration — от чьего имени показывать пост вебхука. Mattermost пишет такой
// пост от создателя вебхука (user_id), а имя и иконку интеграции кладёт в props,
// только если сервер разрешает их подменять (EnablePostUsernameOverride,
// EnablePostIconOverride). Веб-клиент показывает имя и иконку интеграции, а в
// карточке — «пост создан интеграцией от @создатель», без кнопки «Написать».
type Integration struct {
	Name        string // override_username; "" — показываем имя создателя
	IconURL     string // override_icon_url, только http(s); "" — своей иконки нет
	CreatorIcon bool   // use_user_icon: интеграция просит фото создателя
}

// PostIntegration — подменённые имя и иконка поста вебхука. ok=false — пост
// обычный или подмен нет: показываем автора как есть.
func PostIntegration(p *model.Post) (in Integration, ok bool) {
	if p.GetProp(model.PostPropsFromWebhook) != "true" {
		return Integration{}, false
	}
	prop := func(key string) string {
		s, _ := p.GetProp(key).(string)
		return strings.TrimSpace(s)
	}
	in = Integration{
		Name:        prop(model.PostPropsOverrideUsername),
		IconURL:     prop(model.PostPropsOverrideIconURL),
		CreatorIcon: prop("use_user_icon") == "true",
	}
	if u, err := url.Parse(in.IconURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		in.IconURL = ""
	}
	return in, in.Name != "" || in.IconURL != ""
}
