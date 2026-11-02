package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestMentionTerms(t *testing.T) {
	me := &model.User{Username: "alice", FirstName: "Алиса", NotifyProps: model.StringMap{
		model.FirstNameNotifyProp: "true", model.MentionKeysNotifyProp: "alice,@alice,деплой, @channel,Деплой"}}
	if got := mentionTerms(me); got != "@alice Алиса деплой" {
		t.Errorf("terms %q", got)
	}
	if got := mentionTerms(&model.User{Username: "bob"}); got != "@bob" {
		t.Errorf("terms without props %q", got)
	}
}

// «Упоминания»: где меня упомянули — по @имени и своим словам, новые сверху;
// новое упоминание — сразу в открытом списке.
func TestMentions(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		m.me().NotifyProps = model.StringMap{model.MentionKeysNotifyProp: "деплой"}
		bob := fake.UserID("bob")
		backend := channelByName(m, "dev", "backend").Id
		key := fake.Post(&model.Post{ChannelId: backend, UserId: bob, Message: "деплой завершён"})
		fake.Post(&model.Post{ChannelId: backend, UserId: bob, Message: "просто сообщение"})

		found := false
		for _, a := range m.w.Actions() {
			found = found || a.Shortcut().ToString() == "Ctrl+Shift+M"
		}
		if !found {
			t.Error("no Ctrl+Shift+M")
		}
		m.toggleList(listMentions)
		sp := m.search
		if !waitFor(t, "mentions", func() bool { return sp.feed.Has(key.Id) && len(sp.feed.items) >= 4 }) {
			return
		}
		for _, it := range sp.feed.items {
			if msg := strings.ToLower(it.post.Message); !strings.Contains(msg, "@alice") && !strings.Contains(msg, "деплой") {
				t.Errorf("not a mention: %q", it.post.Message)
			}
		}
		if sp.title() != "Упоминания" {
			t.Errorf("title %q", sp.title())
		}
		shot(t, m, "mentions")

		fresh := fake.Post(&model.Post{ChannelId: backend, UserId: bob, Message: "@alice глянь новое"})
		if !waitFor(t, "new mention in the open list", func() bool { return sp.feed.Has(fresh.Id) }) {
			return
		}
		m.toggleList(listMentions)
		if m.listShown(listMentions) {
			t.Error("toggle did not close the list")
		}
	})
}
