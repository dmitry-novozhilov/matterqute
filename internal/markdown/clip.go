package markdown

import (
	"strings"
	"unicode/utf8"
)

// Long — сообщение длинное: в ленте его показываем свёрнутым (Clip), как
// веб-клиент Mattermost («Show more»). Длинное дорого и в разборе HTML, и в
// раскладке: 60 таких постов держали окно секунды.
func Long(s string, maxLines, maxChars int) bool {
	return strings.Count(s, "\n")+1 > maxLines || utf8.RuneCountInString(s) > maxChars
}

// Clip — начало сообщения: не больше maxLines строк и примерно maxChars
// символов, по границе строки; одна длинная строка — по слову, с «…».
// Незакрытый блок кода закрывается, чтобы разметка не съела остальное.
func Clip(s string, maxLines, maxChars int) string {
	var out []string
	n := 0
	fence := ""
	for _, l := range strings.Split(s, "\n") {
		if len(out) >= maxLines {
			break
		}
		r := utf8.RuneCountInString(l)
		if n+r > maxChars {
			if len(out) == 0 {
				out = append(out, cutWords(l, maxChars)+"…")
			}
			break
		}
		out = append(out, l)
		n += r + 1
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			switch {
			case fence == "":
				fence = t[:3]
			case strings.HasPrefix(t, fence):
				fence = ""
			}
		}
	}
	res := strings.Join(out, "\n")
	if fence != "" {
		res += "\n" + fence
	}
	return res
}

// cutWords — начало строки не длиннее max символов, по пробелу (если он есть).
func cutWords(s string, max int) string {
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	cut := string(rs[:max])
	if i := strings.LastIndexByte(cut, ' '); i > len(cut)/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ")
}
