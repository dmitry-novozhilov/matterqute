package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/store"
)

// Предпросмотр ссылок: сайт с картинкой, картинка по ссылке, ссылка на
// сообщение (переход к нему), автор убирает предпросмотр у своего сообщения.
func TestLinkPreviews(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		og, photo := s.ServerURL+"/static/og.png", s.ServerURL+"/static/photo.png"
		meta := func() *model.PostMetadata {
			return &model.PostMetadata{
				Embeds: []*model.PostEmbed{
					{Type: model.PostEmbedOpengraph, URL: "https://example.com/article", Data: map[string]any{
						"title": "Как мы ускорили кэш", "site_name": "Example", "description": "Разбор p99 на /api/v4/posts.",
						"images": []any{map[string]any{"secure_url": og}}}},
					{Type: model.PostEmbedImage, URL: photo},
					{Type: model.PostEmbedPermalink, Data: &model.PreviewPost{PostID: root.Id, Post: root,
						ChannelDisplayName: "Town Square", ChannelID: root.ChannelId, TeamName: "dev"}},
				},
				Images: map[string]*model.PostImage{og: {Width: 1200, Height: 630}, photo: {Width: 640, Height: 360}},
			}
		}
		bob := m.st.UserByName("bob")
		p := fake.Post(&model.Post{ChannelId: root.ChannelId, UserId: bob.Id, Metadata: meta(),
			Message: "Смотрите https://example.com/article и " + photo + " и это сообщение"})
		html := func(id string) string {
			if i := m.feed.index(id); i >= 0 {
				return m.feed.items[i].html
			}
			return ""
		}
		// Картинки догружаются, заглушки сменяются ими.
		if !waitFor(t, "previews with images", func() bool {
			h := html(p.Id)
			return strings.Count(h, `src="link:`) == 2 && !strings.Contains(h, "placeholder:")
		}) {
			t.Logf("html:\n%s", html(p.Id))
			return
		}
		h := html(p.Id)
		for _, want := range []string{"Как мы ускорили кэш", "Example", "Разбор p99", `href="https://example.com/article"`,
			`width="492" height="258"`, // 1200×630 вписана в карточку (520 − полоса 4 − поля 24)
			"Перейти к сообщению", permalinkLink + root.ChannelId + ":" + root.Id} {
			if !strings.Contains(h, want) {
				t.Errorf("preview lacks %q", want)
			}
		}
		m.feed.ScrollToPost(p.Id)
		shot(t, m, "link-previews")

		// «Перейти к сообщению» — прокрутка к нему в открытом канале.
		m.feed.ScrollToPost(p.Id)
		m.feed.onAnchor(permalinkLink + root.ChannelId + ":" + root.Id)
		if got, want := m.feed.view.TextCursor().Position(), m.feed.items[m.feed.index(root.Id)].frame.FirstPosition(); got != want {
			t.Errorf("permalink: cursor at %d, root post starts at %d", got, want)
		}

		// Своё сообщение: автор убирает предпросмотр, ссылка на сообщение остаётся.
		mine := fake.Post(&model.Post{ChannelId: root.ChannelId, UserId: m.s.Me.Id, Metadata: meta(), Message: "моя ссылка https://example.com/article"})
		if !waitFor(t, "own post with preview", func() bool { return strings.Contains(html(mine.Id), "Как мы ускорили кэш") }) {
			return
		}
		if !store.HasLinkPreview(feedPost(m.feed, mine.Id)) {
			t.Fatal("own post has no removable preview")
		}
		m.removePreview(feedPost(m.feed, mine.Id))
		if !waitFor(t, "preview removed", func() bool {
			h := html(mine.Id)
			return h != "" && !strings.Contains(h, "Как мы ускорили кэш") && !strings.Contains(h, `src="link:`)
		}) {
			return
		}
		if !strings.Contains(html(mine.Id), "Перейти к сообщению") {
			t.Error("removing the link preview also removed the permalink")
		}
	})
}
