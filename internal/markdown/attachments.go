package markdown

import (
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

var hexColorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// attachmentColor — цвет полосы слева; именованные — как у Mattermost.
func attachmentColor(c string) string {
	switch c {
	case "good":
		return "#2eb886"
	case "warning":
		return "#daa038"
	case "danger":
		return "#a30200"
	}
	if hexColorRe.MatchString(c) {
		return c
	}
	return "#c8c8c8"
}

// Attachments рисует вложения интеграций (props.attachments, «Slack-вложения»):
// так шлют сообщения боты вроде Grafana, и без этого их посты пустые.
func (r *Renderer) Attachments(atts []*model.MessageAttachment) string {
	var b strings.Builder
	for _, a := range atts {
		if a.Pretext != "" {
			b.WriteString(r.Render(a.Pretext))
		}
		fmt.Fprintf(&b, `<table cellspacing="0" cellpadding="0" style="margin-top:4px;margin-bottom:4px"><tr>`+
			`<td width="4" bgcolor="%s"></td><td><table cellspacing="0" cellpadding="6"><tr><td>`, attachmentColor(a.Color))
		body := r.attachmentBody(a)
		if body == "" {
			body = html.EscapeString(a.Fallback)
		}
		b.WriteString(body)
		b.WriteString("</td></tr></table></td></tr></table>\n")
	}
	return b.String()
}

func (r *Renderer) attachmentBody(a *model.MessageAttachment) string {
	var b strings.Builder
	if a.AuthorName != "" {
		b.WriteString(`<p style="margin:0;color:gray">` + linkOrText(a.AuthorName, a.AuthorLink) + "</p>")
	}
	if a.Title != "" {
		b.WriteString(`<p style="margin:0"><b>` + linkOrText(a.Title, a.TitleLink) + "</b></p>")
	}
	if a.Text != "" {
		b.WriteString(r.Render(a.Text))
	}
	if len(a.Fields) > 0 {
		b.WriteString(`<table cellspacing="0" cellpadding="3" style="margin-top:4px">`)
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
		b.WriteString(`<p style="margin-top:4px;margin-bottom:0;color:gray">` + html.EscapeString(a.Footer) + "</p>")
	}
	return b.String()
}

func (r *Renderer) field(f *model.MessageAttachmentField, span int) string {
	value := ""
	if f.Value != nil {
		value = r.Render(fmt.Sprint(f.Value))
	}
	return fmt.Sprintf(`<td colspan="%d" valign="top"><b>%s</b><br>%s</td>`, span, html.EscapeString(f.Title), value)
}

func linkOrText(text, link string) string {
	if link == "" || !(strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://")) {
		return html.EscapeString(text)
	}
	return `<a href="` + html.EscapeString(link) + `">` + html.EscapeString(text) + "</a>"
}
