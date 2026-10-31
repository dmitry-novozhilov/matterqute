// Package markdown превращает текст сообщения Mattermost в HTML из
// подмножества, которое понимает Qt rich text.
//
// Пока это только экранирование, ссылки и переносы строк; разметка Markdown
// приходит в этап 5 плана.
package markdown

import (
	"html"
	"regexp"
	"strings"
)

var urlRe = regexp.MustCompile(`\bhttps?://[^\s<>"'` + "`" + `]+`)

// Хвостовая пунктуация обычно не часть ссылки: «см. https://x.y/z.».
const urlTrailing = ".,;:!?)]}»"

func Render(text string) string {
	var b strings.Builder
	last := 0
	for _, m := range urlRe.FindAllStringIndex(text, -1) {
		start, end := m[0], m[1]
		for end > start && strings.ContainsRune(urlTrailing, rune(text[end-1])) {
			end--
		}
		b.WriteString(plain(text[last:start]))
		u := text[start:end]
		b.WriteString(`<a href="` + html.EscapeString(u) + `">` + html.EscapeString(u) + `</a>`)
		last = end
	}
	b.WriteString(plain(text[last:]))
	return b.String()
}

func plain(s string) string {
	return strings.ReplaceAll(html.EscapeString(s), "\n", "<br>")
}
