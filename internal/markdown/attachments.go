package markdown

import (
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

var hexColorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// attachmentColor — цвет полосы слева: именованные — семантические цвета темы,
// свой HEX — как есть, остальное — цвет рамок.
func (r *Renderer) attachmentColor(c string) string {
	if named := r.pal.AttachmentColor(c); named != "" {
		return named
	}
	if hexColorRe.MatchString(c) {
		return c
	}
	return r.pal.Line2
}

// Attachments рисует вложения интеграций (props.attachments, «Slack-вложения»):
// так шлют сообщения боты вроде Grafana, и без этого их посты пустые.
func (r *Renderer) Attachments(atts []*model.MessageAttachment) string {
	var b strings.Builder
	for _, a := range atts {
		if a.Pretext != "" {
			b.WriteString(r.Render(a.Pretext))
		}
		// width="520" у таблицы внутри ячейки поста Qt не соблюдает и растягивает
		// её на всю ленту; ширину держит ячейка внешней таблицы с пустой соседкой.
		fmt.Fprintf(&b, `<table cellspacing="0" cellpadding="0" style="margin-top:6px;margin-bottom:2px"><tr><td width="520">`+
			`<table cellspacing="0" cellpadding="0" width="100%%"><tr>`+
			`<td width="4" bgcolor="%s"></td><td bgcolor="%s" style="padding-top:8px;padding-left:12px;padding-right:12px;padding-bottom:10px">`,
			r.attachmentColor(a.Color), r.pal.Surf2)
		body := r.attachmentBody(a)
		if body == "" {
			body = html.EscapeString(a.Fallback)
		}
		b.WriteString(body)
		b.WriteString("</td></tr></table></td><td></td></tr></table>\n")
	}
	return b.String()
}

func (r *Renderer) attachmentBody(a *model.MessageAttachment) string {
	p := r.pal
	var b strings.Builder
	if a.AuthorName != "" {
		fmt.Fprintf(&b, `<p style="margin:0;font-size:12px;color:%s">%s</p>`, p.Tx2, linkOrText(a.AuthorName, a.AuthorLink))
	}
	if a.Title != "" {
		fmt.Fprintf(&b, `<p style="margin:0;font-weight:600">%s</p>`, linkOrText(a.Title, a.TitleLink))
	}
	if a.Text != "" {
		b.WriteString(r.Render(a.Text))
	}
	if len(a.Fields) > 0 {
		b.WriteString(`<table cellspacing="0" cellpadding="0" width="100%" style="margin-top:6px">`)
		// Короткие поля — по два в строку, длинные — на всю ширину.
		for i := 0; i < len(a.Fields); i++ {
			f := a.Fields[i]
			if f.Short && i+1 < len(a.Fields) && a.Fields[i+1].Short {
				b.WriteString("<tr>" + r.field(f, 1) + r.field(a.Fields[i+1], 1) + "</tr>")
				i++
				continue
			}
			b.WriteString("<tr>" + r.field(f, 2) + "</tr>")
		}
		b.WriteString("</table>")
	}
	if a.ImageURL != "" {
		b.WriteString(`<p style="margin:0">` + linkOrText("🖼 изображение", a.ImageURL) + "</p>")
	}
	if a.Footer != "" {
		fmt.Fprintf(&b, `<p style="margin-top:6px;margin-bottom:0;font-size:12px;color:%s">%s</p>`, p.Tx2, html.EscapeString(a.Footer))
	}
	return b.String()
}

func (r *Renderer) field(f *model.MessageAttachmentField, span int) string {
	value := ""
	if f.Value != nil {
		value = r.Render(fmt.Sprint(f.Value))
	}
	width := ""
	if span == 1 {
		width = ` width="50%"`
	}
	return fmt.Sprintf(`<td colspan="%d"%s valign="top" style="padding-bottom:4px"><span style="font-size:12px;font-weight:600;color:%s">%s</span>%s</td>`,
		span, width, r.pal.Tx2, html.EscapeString(f.Title), value)
}

func linkOrText(text, link string) string {
	if link == "" || !(strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://")) {
		return html.EscapeString(text)
	}
	return `<a href="` + html.EscapeString(link) + `">` + html.EscapeString(text) + "</a>"
}
