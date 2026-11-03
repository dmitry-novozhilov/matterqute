package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/mm"
)

// Как показывать имена: выбор — сразу в ленте, сайдбаре, уведомлениях и
// на сервере настройкой; запрет сервера — менять нельзя.
func TestNameFormat(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		bob := m.st.UserByName("bob")
		if f, locked := m.st.NameFormat(); f != model.ShowUsername || locked || m.st.Name(bob.Id) != "bob" {
			t.Errorf("default format %q locked %v, bob shown as %q", f, locked, m.st.Name(bob.Id))
		}
		var post *model.Post
		for _, it := range m.feed.items {
			if it.post.UserId == bob.Id && it.post.Type == "" {
				post = it.post
			}
		}
		if post == nil {
			t.Error("no post from bob in town-square")
			return
		}
		html := func() string { return m.feed.items[m.feed.index(post.Id)].html }
		full := displayName(bob)
		m.setNameFormat(model.ShowFullName)
		if !waitFor(t, "full name in feed", func() bool { return strings.Contains(html(), ">"+full+"</a>") }) {
			return
		}
		var dm *model.Channel
		for _, ch := range m.st.DirectChannels() {
			if ch.Type == model.ChannelTypeDirect && m.st.DMPeer(ch) == bob.Id {
				dm = ch
			}
		}
		if dm == nil || m.st.Title(dm) != full {
			t.Errorf("DM title %q", m.st.Title(dm))
		}
		if n := m.notification(post); !strings.HasPrefix(n.Title, full+" в ") {
			t.Errorf("notification title %q", n.Title)
		}
		if !waitFor(t, "preference on server", func() bool {
			return fake.Pref(m.s.Me.Id, model.PreferenceCategoryDisplaySettings, model.PreferenceNameNameFormat) == model.ShowFullName
		}) {
			return
		}
		shot(t, m, "names-full")
		// Своё эхо (preferences_changed) настройку не сбивает.
		settleFor(300 * time.Millisecond)
		if f, _ := m.st.NameFormat(); f != model.ShowFullName {
			t.Errorf("format after echo %q", f)
		}
		m.setNameFormat(model.ShowUsername)
		waitFor(t, "username in feed", func() bool { return strings.Contains(html(), ">bob</a>") })
	})
}

// Чья настройка главнее: запрет сервера, потом пользователь, потом сервер.
func TestNameFormatChoice(t *testing.T) {
	for _, c := range []struct {
		cfg    map[string]string
		pref   string
		want   string
		locked bool
	}{
		{map[string]string{}, "", model.ShowUsername, false},
		{map[string]string{"TeammateNameDisplay": "full_name"}, "", model.ShowFullName, false},
		{map[string]string{"TeammateNameDisplay": "full_name"}, "nickname_full_name", model.ShowNicknameFullName, false},
		{map[string]string{"TeammateNameDisplay": "full_name", "LockTeammateNameDisplay": "true"}, "username", model.ShowFullName, true},
		{map[string]string{"TeammateNameDisplay": "что-то"}, "тоже", model.ShowUsername, false},
	} {
		if f, locked := mm.NameFormat(c.cfg, c.pref); f != c.want || locked != c.locked {
			t.Errorf("NameFormat(%v, %q) = %q, %v; want %q, %v", c.cfg, c.pref, f, locked, c.want, c.locked)
		}
	}
}
