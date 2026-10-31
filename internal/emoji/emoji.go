// Package emoji — имена эмодзи Mattermost ("+1", "tada") ↔ символы.
package emoji

import (
	"strconv"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// Quick — эмодзи для быстрого выбора реакции.
var Quick = []string{"+1", "heart", "joy", "tada", "eyes", "white_check_mark", "pray", "fire", "rocket", "thinking_face", "100", "-1"}

// Char — символ системного эмодзи по имени; для кастомных эмодзи сервера false.
func Char(name string) (string, bool) {
	code, ok := model.SystemEmojis[name]
	if !ok {
		return "", false
	}
	var b strings.Builder
	for _, part := range strings.Split(code, "-") {
		r, err := strconv.ParseUint(part, 16, 32)
		if err != nil {
			return "", false
		}
		b.WriteRune(rune(r))
	}
	return b.String(), true
}

// Display — символ, а для кастомных эмодзи :имя:.
func Display(name string) string {
	if s, ok := Char(name); ok {
		return s
	}
	return ":" + name + ":"
}
