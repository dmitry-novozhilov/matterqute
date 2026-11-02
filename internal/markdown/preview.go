package markdown

import (
	"regexp"
	"strings"
)

var (
	blockMarkerRe = regexp.MustCompile(`^(#{1,6}\s+|>\s?|[-*+]\s+(\[[ xX]\]\s+)?|\d{1,9}[.)]\s+)`)
	thematicRe    = regexp.MustCompile(`^([-*_]\s*){3,}$`)
)

// PreviewLine — первая строка сообщения одной строкой HTML с оформлением
// (список тредов): маркеры списка, цитаты и заголовка убраны, из блока кода —
// первая строка кода. Пусто — в сообщении нет текста.
func PreviewLine(src string, o Options) string {
	inCode := false
	for _, l := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inCode = !inCode
			continue
		}
		if t == "" || !inCode && thematicRe.MatchString(t) {
			continue
		}
		if inCode {
			t = codeSpan(t)
		} else {
			for {
				s := blockMarkerRe.ReplaceAllString(t, "")
				if s == t {
					break
				}
				t = strings.TrimSpace(s)
			}
			if t == "" {
				continue
			}
		}
		out := New(o).Render(t)
		out = strings.TrimPrefix(out, `<p style="margin:0">`)
		return strings.TrimSuffix(out, "</p>")
	}
	return ""
}
