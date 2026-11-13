package ui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"strings"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/markdown"
	"matterqute/internal/store"
)

const (
	permalinkLink = "mm-permalink:" // mm-permalink:<канал>:<пост> — перейти к сообщению
	cardWidth     = 520             // карточки предпросмотра — как вложения интеграций
	cardInner     = cardWidth - 4 - 24
)

// LinkImage — картинка предпросмотра ссылки на диске или "".
func (a *Assets) LinkImage(rawURL string) string { return a.c.LinkImagePath(rawURL) }

// WantLinkImage догружает картинку предпросмотра; done — когда она на диске.
func (a *Assets) WantLinkImage(rawURL string, done func()) {
	a.want("link:"+rawURL, done, func(ctx context.Context) error {
		_, err := a.c.FetchLinkImage(ctx, rawURL)
		return err
	})
}

// embedsHTML — предпросмотры ссылок под сообщением: сайт (OpenGraph),
// картинка по ссылке, ссылка на другое сообщение.
func (f *Feed) embedsHTML(p *model.Post) string {
	withLinks := f.d.LinkPreviews == nil || f.d.LinkPreviews()
	var b strings.Builder
	images := 0
	for _, pv := range store.PostPreviews(p, withLinks) {
		switch pv.Kind {
		case model.PostEmbedOpengraph:
			b.WriteString(f.openGraphHTML(pv))
		case model.PostEmbedImage:
			// Клик — просмотр во весь экран (MainWindow.viewerItems считает так же).
			w, h := f.linkImageSize(pv, f.columnWidth(), previewMaxH)
			fmt.Fprintf(&b, `<p style="margin-top:6px;margin-bottom:0"><a href="%s%s:%d"><img src="%s" width="%d" height="%d" title="%s"></a></p>`,
				linkImageLink, p.Id, images, f.linkImageSrc(pv.Image, w, h), w, h, html.EscapeString(pv.URL))
			images++
		case model.PostEmbedPermalink:
			b.WriteString(f.permalinkHTML(pv))
		}
	}
	return b.String()
}

// previewCard — полоса слева и содержимое на фоне surf2, ширина как у вложений.
// Ширину держит ячейка внешней таблицы: width у таблицы внутри ячейки поста
// Qt не соблюдает (см. Attachments).
func previewCard(bar, bg, content string) string {
	return fmt.Sprintf(`<table cellspacing="0" cellpadding="0" style="margin-top:6px;margin-bottom:2px"><tr><td width="%d">`+
		`<table cellspacing="0" cellpadding="0" width="100%%"><tr>`+
		`<td width="4" bgcolor="%s"></td><td bgcolor="%s" style="padding-top:8px;padding-left:12px;padding-right:12px;padding-bottom:10px">%s</td>`+
		`</tr></table></td><td></td></tr></table>`, cardWidth, bar, bg, content)
}

func (f *Feed) openGraphHTML(pv store.Preview) string {
	pal := Pal()
	var b strings.Builder
	fmt.Fprintf(&b, `<p style="margin:0;font-size:12px;color:%s">%s</p>`, pal.Tx2, html.EscapeString(pv.Site))
	fmt.Fprintf(&b, `<p style="margin:0;font-weight:600"><a href="%s">%s</a></p>`, html.EscapeString(pv.URL), html.EscapeString(pv.Title))
	if d := firstRunes(pv.Description, 300); d != "" {
		fmt.Fprintf(&b, `<p style="margin-top:2px;margin-bottom:0;font-size:13px">%s</p>`, html.EscapeString(d))
	}
	if pv.Image != "" {
		w, h := f.linkImageSize(pv, cardInner, 260)
		fmt.Fprintf(&b, `<p style="margin-top:8px;margin-bottom:0"><a href="%s"><img src="%s" width="%d" height="%d"></a></p>`,
			html.EscapeString(pv.URL), f.linkImageSrc(pv.Image, w, h), w, h)
	}
	return previewCard(pal.Line2, pal.Surf2, b.String())
}

func (f *Feed) permalinkHTML(pv store.Preview) string {
	pal := Pal()
	author := f.d.Names(pv.Post.UserId)
	where := pv.ChannelName
	if where != "" {
		where = " · # " + where
	}
	text := strings.ReplaceAll(html.EscapeString(firstRunes(markdown.PlainText(pv.Post.Message), 300)), "\n", "<br>")
	content := fmt.Sprintf(`<p style="margin:0;font-size:12px;color:%s"><b style="color:%s">%s</b>%s</p>`+
		`<p style="margin-top:2px;margin-bottom:0">%s</p>`+
		`<p style="margin-top:4px;margin-bottom:0;font-size:12px"><a href="%s%s:%s" style="color:%s">Перейти к сообщению</a></p>`,
		pal.Tx2, pal.Tx, html.EscapeString(author), html.EscapeString(where), text,
		permalinkLink, pv.ChannelID, pv.Post.Id, pal.Acc)
	return previewCard(pal.Acc, pal.Surf2, content)
}

// linkImageSize — размер картинки предпросмотра: не увеличиваем, вписываем в
// maxW×maxH. Размер неизвестен — по файлу, если он уже скачан, иначе 240×160.
func (f *Feed) linkImageSize(pv store.Preview, maxW, maxH int) (int, int) {
	w, h := pv.ImageW, pv.ImageH
	if (w <= 0 || h <= 0) && f.d.Assets != nil {
		if path := f.d.Assets.LinkImage(pv.Image); path != "" {
			w, h = imageFileSize(path)
		}
	}
	if w <= 0 || h <= 0 {
		return min(240, maxW), 160
	}
	w, h = fitSize(w, h, maxW, maxH)
	return max(w, 24), max(h, 24)
}

// linkImageSrc — ресурс с картинкой предпросмотра или заглушка того же
// размера, пока она грузится. Адрес — чужой текст: в имени ресурса только хэш.
func (f *Feed) linkImageSrc(rawURL string, w, h int) string {
	sum := sha256.Sum256([]byte(rawURL))
	name := fmt.Sprintf("link:%s:%dx%d", hex.EncodeToString(sum[:12]), w, h)
	if _, ok := f.resources[name]; ok {
		return name
	}
	if f.d.Assets != nil {
		if path := f.d.Assets.LinkImage(rawURL); path != "" {
			f.addLazy(name, w, h, lazyImage{load: func() *qt.QPixmap { return scaledFile(path, w, h) }})
			return name
		}
		f.d.Assets.WantLinkImage(rawURL, f.refreshLater())
	}
	return f.placeholderSrc(w, h)
}

// placeholderSrc — серая заглушка w×h (пока картинка грузится): точка 1×1,
// Qt растягивает её на место картинки.
func (f *Feed) placeholderSrc(w, h int) string {
	ph := fmt.Sprintf("placeholder:%dx%d", w, h)
	if _, ok := f.resources[ph]; !ok {
		f.addResource(ph, lazyDot(), w, h)
	}
	return ph
}

// imageFileSize — размер картинки в файле по заголовку, без разбора всей
// картинки (её мегабайты не нужны); не прочитался — 0, 0.
func imageFileSize(path string) (w, h int) {
	r := qt.NewQImageReader3(path)
	defer free(r)
	if sz := tmp(r.Size()); sz.IsValid() {
		return sz.Width(), sz.Height()
	}
	img := tmp(r.Read()) // формат без размера в заголовке
	return img.Width(), img.Height()
}

// columnWidth — ширина колонки текста в ленте.
func (f *Feed) columnWidth() int {
	if w := f.view.Viewport().Width() - feedAvatarCol - feedPadRight; w >= 100 {
		return w
	}
	return 600
}

func firstRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// removePreview — автор убирает предпросмотр ссылки у своего сообщения (как
// веб-клиент: свойство remove_link_preview).
func (m *MainWindow) removePreview(p *model.Post) {
	props := model.StringInterface{}
	for k, v := range p.GetProps() {
		props[k] = v
	}
	props[store.RemoveLinkPreview] = "true"
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		edited, _, err := m.s.Client.PatchPost(ctx, p.Id, &model.PostPatch{Props: &props})
		m.ui(func() {
			if err != nil {
				m.fail("remove link preview", fmt.Errorf("patch post %s: %w", p.Id, err))
				return
			}
			m.applyEdited(edited) // то же придёт по WebSocket
		})
	}()
}
