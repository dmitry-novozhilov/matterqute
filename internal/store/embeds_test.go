package store

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// Посты приходят JSON-ом: Data вложения — map, как после разбора ответа сервера.
func decodePost(t *testing.T, raw string) *model.Post {
	t.Helper()
	var p model.Post
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	return &p
}

func TestPostPreviews(t *testing.T) {
	p := decodePost(t, `{"id":"p","message":"глянь https://example.com/a и https://img.example.com/x.png и ссылку на пост",
	"metadata":{
		"embeds":[
			{"type":"opengraph","url":"https://example.com/a","data":{"title":"Статья","description":"Про кэш","site_name":"",
				"images":[{"url":"http://example.com/og.png","secure_url":"https://example.com/og.png","width":0,"height":0}]}},
			{"type":"image","url":"https://img.example.com/x.png"},
			{"type":"permalink","data":{"post_id":"q","post":{"id":"q","user_id":"u","message":"исходное"},"channel_display_name":"Town Square","channel_id":"c","team_name":"dev"}},
			{"type":"opengraph","url":"https://empty.example.com","data":{"title":""}},
			{"type":"image","url":"javascript:alert(1)"}
		],
		"images":{"https://example.com/og.png":{"width":1200,"height":630},"https://img.example.com/x.png":{"width":640,"height":360}}
	}}`)
	got := PostPreviews(p, true)
	if len(got) != 3 {
		t.Fatalf("%d previews, want opengraph, image and permalink: %+v", len(got), got)
	}
	og, img, link := got[0], got[1], got[2]
	if og.Kind != model.PostEmbedOpengraph || og.Title != "Статья" || og.Site != "example.com" || og.Image != "https://example.com/og.png" || og.ImageW != 1200 || og.ImageH != 630 {
		t.Errorf("opengraph %+v", og)
	}
	if img.Kind != model.PostEmbedImage || img.Image != "https://img.example.com/x.png" || img.ImageW != 640 {
		t.Errorf("image %+v", img)
	}
	if link.Kind != model.PostEmbedPermalink || link.Post.Message != "исходное" || link.ChannelID != "c" || link.ChannelName != "Town Square" {
		t.Errorf("permalink %+v", link)
	}
	if !HasLinkPreview(p) {
		t.Error("HasLinkPreview = false")
	}

	// Предпросмотр выключен — остаются ссылки на сообщения; автор убрал — тоже.
	if got := PostPreviews(p, false); len(got) != 1 || got[0].Kind != model.PostEmbedPermalink {
		t.Errorf("previews off: %+v", got)
	}
	p.AddProp(RemoveLinkPreview, "true")
	if got := PostPreviews(p, true); len(got) != 1 || got[0].Kind != model.PostEmbedPermalink || HasLinkPreview(p) {
		t.Errorf("removed by author: %+v", got)
	}
	if PostPreviews(&model.Post{}, true) != nil {
		t.Error("post without metadata has previews")
	}
}
