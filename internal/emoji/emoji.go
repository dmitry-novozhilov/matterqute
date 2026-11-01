// Package emoji — имена эмодзи Mattermost ("+1", "tada") ↔ символы.
package emoji

import (
	"slices"
	"strconv"
	"strings"
	"sync"

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
	// В составных эмодзи (через ZWJ) селектор fe0f Qt 6.8 с Noto Color Emoji
	// не переваривает: 🏳️‍🌈 рисуется флагом и радугой рядом. Без него символ
	// собирается; у одиночных fe0f оставляем — он просит цветное начертание.
	if strings.Contains(code, "200d") {
		code = normCode(code)
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

// Names — имена всех системных эмодзи по алфавиту (для автодополнения :).
var Names = sync.OnceValue(func() []string {
	out := make([]string, 0, len(model.SystemEmojis))
	for name := range model.SystemEmojis {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
})
