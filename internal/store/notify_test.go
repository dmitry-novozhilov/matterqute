package store

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestShouldNotify(t *testing.T) {
	type opts struct {
		userLevel, chLevel string // notify_props.desktop профиля и канала
		muted, crt         bool
		status             string // наш статус
		chType             model.ChannelType
		known              bool // канал и членство загружены
	}
	newStore := func(o opts) *Store {
		me := &model.User{Id: "me", Username: "me", NotifyProps: model.StringMap{}}
		if o.userLevel != "" {
			me.NotifyProps[model.DesktopNotifyProp] = o.userLevel
		}
		s := New(me)
		if o.status != "" {
			s.SetStatus("me", o.status)
		}
		if o.chType == "" {
			o.chType = model.ChannelTypeOpen
		}
		sb := &Sidebar{CRT: o.crt}
		if o.known {
			m := &model.ChannelMember{ChannelId: "c", UserId: "me", NotifyProps: model.StringMap{}}
			if o.chLevel != "" {
				m.NotifyProps[model.DesktopNotifyProp] = o.chLevel
			}
			if o.muted {
				m.NotifyProps[model.MarkUnreadNotifyProp] = model.ChannelMarkUnreadMention
			}
			sb.Channels = []*model.Channel{{Id: "c", Type: o.chType}}
			sb.Members = []*model.ChannelMember{m}
		}
		s.SetSidebar(sb)
		return s
	}
	post := func(user, root string) *model.Post {
		return &model.Post{Id: "p", ChannelId: "c", UserId: user, RootId: root, Message: "hi"}
	}
	hook := post("me", "")
	hook.AddProp(model.PostPropsFromWebhook, "true")
	system := post("bob", "")
	system.Type = model.PostTypeJoinChannel

	for _, tc := range []struct {
		name string
		o    opts
		e    NewPost
		want bool
	}{
		{"обычный пост, всё по умолчанию", opts{known: true}, NewPost{Post: post("bob", "")}, true},
		{"свой пост", opts{known: true}, NewPost{Post: post("me", "")}, false},
		{"свой вебхук", opts{known: true}, NewPost{Post: hook}, true},
		{"системное", opts{known: true}, NewPost{Post: system}, false},
		{"заглушён, даже с упоминанием", opts{known: true, muted: true}, NewPost{Post: post("bob", ""), Mentioned: true}, false},
		{"не беспокоить", opts{known: true, status: model.StatusDnd}, NewPost{Post: post("bob", ""), Mentioned: true}, false},
		{"нет на работе", opts{known: true, status: model.StatusOutOfOffice}, NewPost{Post: post("bob", "")}, false},
		{"профиль: упоминания, без упоминания", opts{known: true, userLevel: "mention"}, NewPost{Post: post("bob", "")}, false},
		{"профиль: упоминания, с упоминанием", opts{known: true, userLevel: "mention"}, NewPost{Post: post("bob", ""), Mentioned: true}, true},
		{"профиль: упоминания, личка", opts{known: true, userLevel: "mention", chType: model.ChannelTypeDirect}, NewPost{Post: post("bob", "")}, true},
		{"профиль: упоминания, группа по умолчанию — все", opts{known: true, userLevel: "mention", chType: model.ChannelTypeGroup}, NewPost{Post: post("bob", "")}, true},
		{"профиль: упоминания, канал: все", opts{known: true, userLevel: "mention", chLevel: "all"}, NewPost{Post: post("bob", "")}, true},
		{"профиль: все, канал: ничего", opts{known: true, userLevel: "all", chLevel: "none"}, NewPost{Post: post("bob", ""), Mentioned: true}, false},
		{"профиль: ничего, канал по умолчанию", opts{known: true, userLevel: "none", chLevel: "default"}, NewPost{Post: post("bob", "")}, false},
		{"CRT: ответ в чужом треде", opts{known: true, crt: true}, NewPost{Post: post("bob", "root")}, false},
		{"CRT: ответ в своём треде", opts{known: true, crt: true}, NewPost{Post: post("bob", "root"), Following: true}, true},
		{"CRT: упоминание в чужом треде", opts{known: true, crt: true}, NewPost{Post: post("bob", "root"), Mentioned: true}, true},
		{"без CRT: ответ — как пост", opts{known: true}, NewPost{Post: post("bob", "root")}, true},
		{"неизвестный канал", opts{}, NewPost{Post: post("bob", ""), ChannelType: "O"}, false},
		{"новая личка", opts{}, NewPost{Post: post("bob", ""), ChannelType: "D"}, true},
	} {
		if got := newStore(tc.o).ShouldNotify(tc.e); got != tc.want {
			t.Errorf("%s: ShouldNotify = %v, want %v", tc.name, got, tc.want)
		}
	}
}
