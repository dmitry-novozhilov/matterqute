package emoji

import (
	"cmp"
	"slices"
	"strings"
	"sync"

	"github.com/mattermost/mattermost/server/public/model"
)

//go:generate go run ./gen -in emoji-test.txt -out groups_gen.go

type unicodeGroup struct {
	name  string
	codes []string // коды как в model.SystemEmojis, полной формой (с fe0f)
}

// Category — раздел окна выбора эмодзи.
type Category struct {
	Title string
	Names []string // имена Mattermost в порядке Unicode (CLDR)
}

var groupTitles = map[string]string{
	"Smileys & Emotion": "Смайлы и эмоции",
	"People & Body":     "Люди",
	"Animals & Nature":  "Животные и природа",
	"Food & Drink":      "Еда и напитки",
	"Travel & Places":   "Путешествия и места",
	"Activities":        "Занятия",
	"Objects":           "Предметы",
	"Symbols":           "Символы",
	"Flags":             "Флаги",
}

func normCode(code string) string {
	return strings.ReplaceAll(strings.ReplaceAll(code, "-fe0f", ""), "fe0f-", "")
}

// canonical — по коду символа (без fe0f) одно имя: самое короткое, при
// равенстве — первое по алфавиту («+1», а не «thumbsup»).
var canonical = sync.OnceValue(func() map[string]string {
	out := map[string]string{}
	for name, code := range model.SystemEmojis {
		c := normCode(code)
		if old, ok := out[c]; !ok || len(name) < len(old) || len(name) == len(old) && name < old {
			out[c] = name
		}
	}
	return out
})

// Categories — системные эмодзи по разделам Unicode. Символов без имени в
// Mattermost нет: реакцию ими сервер не примет.
var Categories = sync.OnceValue(func() []Category {
	names := canonical()
	var out []Category
	for _, g := range unicodeGroups {
		c := Category{Title: groupTitles[g.name]}
		for _, code := range g.codes {
			if n, ok := names[normCode(code)]; ok {
				c.Names = append(c.Names, n)
			}
		}
		out = append(out, c)
	}
	return out
})

// Search — эмодзи, у которых какое-то из имён содержит запрос, по одному на
// символ: сначала те, где имя начинается с запроса. custom — имена эмодзи сервера.
func Search(query string, custom []string, limit int) []string {
	q := strings.ToLower(strings.Trim(query, ": "))
	if q == "" {
		return nil
	}
	type hit struct {
		name   string
		prefix bool
		match  string // имя, по которому нашли: короче — выше
	}
	byName := map[string]*hit{}
	add := func(display, matched string) {
		prefix := strings.HasPrefix(matched, q)
		if !prefix && !strings.Contains(matched, q) {
			return
		}
		h := byName[display]
		if h == nil || prefix && !h.prefix || prefix == h.prefix && len(matched) < len(h.match) {
			byName[display] = &hit{display, prefix, matched}
		}
	}
	names := canonical()
	for name, code := range model.SystemEmojis {
		add(names[normCode(code)], name)
	}
	for _, name := range custom {
		add(name, name)
	}
	hits := make([]*hit, 0, len(byName))
	for _, h := range byName {
		hits = append(hits, h)
	}
	slices.SortFunc(hits, func(a, b *hit) int {
		if a.prefix != b.prefix {
			if a.prefix {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(len(a.match), len(b.match)), cmp.Compare(a.name, b.name))
	})
	out := make([]string, 0, min(len(hits), limit))
	for _, h := range hits[:min(len(hits), limit)] {
		out = append(out, h.name)
	}
	return out
}

// RecentCategory и MaxRecent — недавние эмодзи хранятся как у веб-клиента:
// настройка recent_emojis (имя — id пользователя), JSON [{name, usageCount}]
// по возрастанию числа использований, не больше 27.
const (
	RecentCategory = "recent_emojis"
	MaxRecent      = 27
)

type RecentEmoji struct {
	Name       string `json:"name"`
	UsageCount int    `json:"usageCount"`
}

type Recent []RecentEmoji

// Add — эмодзи использовали: как addRecentEmojis веб-клиента — в конец со
// счётчиком +1, обрезать до 27 с начала, упорядочить по числу использований.
func (r Recent) Add(name string) Recent {
	out := slices.Clone(r)
	count := 1
	if i := slices.IndexFunc(out, func(e RecentEmoji) bool { return e.Name == name }); i >= 0 {
		count = out[i].UsageCount + 1
		out = slices.Delete(out, i, i+1)
	}
	out = append(out, RecentEmoji{name, count})
	if len(out) > MaxRecent {
		out = out[len(out)-MaxRecent:]
	}
	slices.SortStableFunc(out, func(a, b RecentEmoji) int { return cmp.Compare(a.UsageCount, b.UsageCount) })
	return out
}

// Names — для окна выбора: самые частые первыми.
func (r Recent) Names() []string {
	out := make([]string, 0, len(r))
	for i := len(r) - 1; i >= 0; i-- {
		out = append(out, r[i].Name)
	}
	return out
}
