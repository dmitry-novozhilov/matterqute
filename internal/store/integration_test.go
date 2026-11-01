package store

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestPostIntegration(t *testing.T) {
	hook := func(props map[string]any) *model.Post {
		p := &model.Post{UserId: "creator"}
		p.AddProp(model.PostPropsFromWebhook, "true")
		for k, v := range props {
			p.AddProp(k, v)
		}
		return p
	}
	for _, tc := range []struct {
		name string
		post *model.Post
		want Integration
		ok   bool
	}{
		{"обычный пост", &model.Post{UserId: "u"}, Integration{}, false},
		{"override без from_webhook", func() *model.Post {
			p := &model.Post{UserId: "u"}
			p.AddProp(model.PostPropsOverrideUsername, "Grafana")
			return p
		}(), Integration{}, false},
		{"вебхук без подмен", hook(nil), Integration{}, false},
		{"имя", hook(map[string]any{model.PostPropsOverrideUsername: "Grafana"}), Integration{Name: "Grafana"}, true},
		{"имя и иконка", hook(map[string]any{model.PostPropsOverrideUsername: "Grafana", model.PostPropsOverrideIconURL: "https://grafana.example.com/icon.png"}),
			Integration{Name: "Grafana", IconURL: "https://grafana.example.com/icon.png"}, true},
		{"иконка не http", hook(map[string]any{model.PostPropsOverrideUsername: "x", model.PostPropsOverrideIconURL: "file:///etc/passwd"}), Integration{Name: "x"}, true},
		{"иконка без хоста", hook(map[string]any{model.PostPropsOverrideIconURL: "/static/icon.png"}), Integration{}, false},
		{"фото создателя", hook(map[string]any{model.PostPropsOverrideUsername: "CI", "use_user_icon": "true"}), Integration{Name: "CI", CreatorIcon: true}, true},
		{"имя не строкой", hook(map[string]any{model.PostPropsOverrideUsername: 42}), Integration{}, false},
	} {
		got, ok := PostIntegration(tc.post)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: got %+v %v, want %+v %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}
