package store

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// RemoveLinkPreview — свойство поста: автор убрал предпросмотр ссылки (как
// PostTypes.REMOVE_LINK_PREVIEW веб-клиента).
const RemoveLinkPreview = "remove_link_preview"

// Preview — что показать под сообщением со ссылкой. Данные добывает сервер
// (EnableLinkPreviews) и кладёт в post.metadata.embeds.
type Preview struct {
	Kind model.PostEmbedType // opengraph, image, permalink
	URL  string              // куда ведёт ссылка

	// opengraph
	Site, Title, Description string
	// opengraph и image: картинка и её размер (0 — неизвестен)
	Image          string
	ImageW, ImageH int

	// permalink: сообщение, на которое ссылаются
	Post        *model.Post
	ChannelID   string
	ChannelName string // название канала
	TeamName    string
}

type ogImage struct {
	URL       string `json:"url"`
	SecureURL string `json:"secure_url"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

type ogData struct {
	URL         string    `json:"url"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	SiteName    string    `json:"site_name"`
	Images      []ogImage `json:"images"`
}

// PostPreviews — предпросмотры ссылок поста. withLinks=false (пользователь
// выключил предпросмотр) — только ссылки на сообщения. Автор мог убрать
// предпросмотр у своего поста (remove_link_preview).
func PostPreviews(p *model.Post, withLinks bool) []Preview {
	if p.Metadata == nil {
		return nil
	}
	removed := false
	switch v := p.GetProp(RemoveLinkPreview).(type) {
	case string:
		removed = v == "true"
	case bool:
		removed = v
	}
	var out []Preview
	for _, e := range p.Metadata.Embeds {
		if e == nil {
			continue
		}
		switch e.Type {
		case model.PostEmbedOpengraph:
			if !withLinks || removed {
				continue
			}
			var og ogData
			if !remarshal(e.Data, &og) || og.Title == "" {
				continue
			}
			pv := Preview{Kind: e.Type, URL: cmpOr(og.URL, e.URL), Site: og.SiteName, Title: og.Title, Description: og.Description}
			if pv.Site == "" {
				if u, err := url.Parse(pv.URL); err == nil {
					pv.Site = strings.TrimPrefix(u.Host, "www.")
				}
			}
			for _, img := range og.Images {
				if src := cmpOr(img.SecureURL, img.URL); webURL(src) {
					pv.Image, pv.ImageW, pv.ImageH = src, img.Width, img.Height
					if mi := p.Metadata.Images[src]; mi != nil && (pv.ImageW == 0 || pv.ImageH == 0) {
						pv.ImageW, pv.ImageH = mi.Width, mi.Height
					}
					break
				}
			}
			out = append(out, pv)
		case model.PostEmbedImage:
			if !withLinks || removed || !webURL(e.URL) {
				continue
			}
			pv := Preview{Kind: e.Type, URL: e.URL, Image: e.URL}
			if mi := p.Metadata.Images[e.URL]; mi != nil {
				pv.ImageW, pv.ImageH = mi.Width, mi.Height
			}
			out = append(out, pv)
		case model.PostEmbedPermalink:
			var pp model.PreviewPost
			if !remarshal(e.Data, &pp) || pp.Post == nil {
				continue
			}
			out = append(out, Preview{Kind: e.Type, URL: e.URL, Post: pp.Post, ChannelID: pp.ChannelID,
				ChannelName: pp.ChannelDisplayName, TeamName: pp.TeamName})
		}
	}
	return out
}

// HasLinkPreview — у поста есть предпросмотр сайта или картинки, который автор может убрать.
func HasLinkPreview(p *model.Post) bool {
	for _, pv := range PostPreviews(p, true) {
		if pv.Kind != model.PostEmbedPermalink {
			return true
		}
	}
	return false
}

func remarshal(in, out any) bool {
	b, err := json.Marshal(in)
	return err == nil && json.Unmarshal(b, out) == nil
}

func webURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
