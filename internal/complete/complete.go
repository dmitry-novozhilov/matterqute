// Package complete — автодополнение в поле ввода: что набирают после @, # или
// ~ и :, и какие пользователи, каналы и эмодзи под это подходят. Без Qt: поле
// ввода отдаёт текст до курсора, получает, что предложить и что заменить.
package complete

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/mattermost/mattermost/server/public/model"
)

// Kind — что дополняем.
type Kind int

const (
	None    Kind = iota
	User         // @имя
	Channel      // ~канал (и #канал — вставляем всё равно ~канал)
	Emoji        // :имя:
	Command      // /команда с аргументами — подсказывает сервер
)

// Token — слово под курсором, которое дополняем.
type Token struct {
	Kind  Kind
	Query string // что набрано после знака, как есть
	Len   int    // длина слова со знаком в UTF-16 (позиции QTextCursor): столько заменить
}

// emojiMinQuery — эмодзи предлагаем с двух букв, как веб-клиент: иначе «:)» и
// время «10:30» открывали бы список.
const emojiMinQuery = 2

// At — что дополнять, если до курсора стоит textBefore (текст строки до
// курсора). Слово начинается знаком после пробела или с начала строки.
func At(textBefore string) (Token, bool) {
	start := strings.LastIndexFunc(textBefore, func(r rune) bool { return unicode.IsSpace(r) || r == '(' }) + 1
	if start > 0 {
		// LastIndexFunc вернул байт начала разделителя; пропускаем его целиком.
		_, size := lastRune(textBefore[:start])
		start += size - 1
	}
	word := textBefore[start:]
	if word == "" {
		return Token{}, false
	}
	t := Token{Query: word[1:], Len: utf16Len(word)}
	switch word[0] {
	case '@':
		t.Kind = User
		if !validQuery(t.Query, func(r rune) bool { return isNameRune(r) || r == '.' || r == '-' || r == '_' }) {
			return Token{}, false
		}
	case '~', '#':
		t.Kind = Channel
		if strings.ContainsAny(t.Query, "~#@:") {
			return Token{}, false
		}
	case ':':
		t.Kind = Emoji
		if len(t.Query) < emojiMinQuery || !validQuery(t.Query, func(r rune) bool {
			return r < 128 && (isNameRune(r) || r == '_' || r == '+' || r == '-')
		}) {
			return Token{}, false
		}
	default:
		return Token{}, false
	}
	return t, true
}

// CommandAt — команда: сообщение начинается с «/» (до курсора — первая строка
// поля). Query — всё после «/» вместе с аргументами: подсказки по ним даёт
// сервер. «//» — не команда.
func CommandAt(textBefore string) (Token, bool) {
	if !strings.HasPrefix(textBefore, "/") || strings.HasPrefix(textBefore, "//") || strings.ContainsAny(textBefore, "\n\u2028") {
		return Token{}, false
	}
	return Token{Kind: Command, Query: textBefore[1:], Len: utf16Len(textBefore)}, true
}

func lastRune(s string) (rune, int) {
	for i := len(s) - 1; i >= 0; i-- {
		if r := []rune(s[i:]); len(r) == 1 {
			return r[0], len(s) - i
		}
	}
	return 0, 1
}

func isNameRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func validQuery(q string, ok func(rune) bool) bool {
	for _, r := range q {
		if !ok(r) {
			return false
		}
	}
	return true
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// matchWords — запрос — начало какого-то слова в одной из строк (регистр не важен).
func matchWords(q string, fields ...string) bool {
	for _, f := range fields {
		for _, w := range strings.FieldsFunc(strings.ToLower(f), func(r rune) bool {
			return unicode.IsSpace(r) || r == '-' || r == '_' || r == '.'
		}) {
			if strings.HasPrefix(w, q) {
				return true
			}
		}
		if strings.HasPrefix(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

// Users — кто подходит под @query: по имени пользователя, имени, фамилии,
// нику. Сначала те, у кого с запроса начинается имя пользователя, дальше по алфавиту.
func Users(users []*model.User, query string, limit int) []*model.User {
	q := strings.ToLower(query)
	var out []*model.User
	seen := map[string]bool{}
	for _, u := range users {
		if u == nil || u.DeleteAt > 0 || seen[u.Id] {
			continue
		}
		if matchWords(q, u.Username, u.FirstName, u.LastName, u.Nickname, u.FirstName+" "+u.LastName) {
			out = append(out, u)
			seen[u.Id] = true
		}
	}
	slices.SortStableFunc(out, func(a, b *model.User) int {
		pa, pb := strings.HasPrefix(a.Username, q), strings.HasPrefix(b.Username, q)
		if pa != pb {
			if pa {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Username, b.Username)
	})
	return out[:min(len(out), limit)]
}

// Special — @here, @channel, @all: в канале, не в личке.
type Special struct{ Name, Description string }

var specials = []Special{
	{"here", "Уведомить всех, кто в сети в этом канале"},
	{"channel", "Уведомить всех в этом канале"},
	{"all", "Уведомить всех в этом канале"},
}

// Specials — общие упоминания, подходящие под запрос.
func Specials(query string) []Special {
	var out []Special
	for _, s := range specials {
		if strings.HasPrefix(s.Name, strings.ToLower(query)) {
			out = append(out, s)
		}
	}
	return out
}

// Channels — каналы под ~query: по имени и названию; личные и групповые
// упомянуть нельзя. Сначала те, у кого с запроса начинается имя.
func Channels(chs []*model.Channel, query string, limit int) []*model.Channel {
	q := strings.ToLower(query)
	var out []*model.Channel
	seen := map[string]bool{}
	for _, ch := range chs {
		if ch == nil || ch.IsGroupOrDirect() || ch.DeleteAt > 0 || seen[ch.Id] {
			continue
		}
		if matchWords(q, ch.Name, ch.DisplayName) {
			out = append(out, ch)
			seen[ch.Id] = true
		}
	}
	slices.SortStableFunc(out, func(a, b *model.Channel) int {
		pa, pb := strings.HasPrefix(a.Name, q), strings.HasPrefix(b.Name, q)
		if pa != pb {
			if pa {
				return -1
			}
			return 1
		}
		return cmp.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName))
	})
	return out[:min(len(out), limit)]
}

// EmojiNames — имена эмодзи под :query: сначала начинающиеся с запроса (короткие
// раньше), потом содержащие его.
func EmojiNames(names []string, query string, limit int) []string {
	q := strings.ToLower(query)
	var prefix, inside []string
	for _, n := range names {
		switch {
		case strings.HasPrefix(n, q):
			prefix = append(prefix, n)
		case strings.Contains(n, q):
			inside = append(inside, n)
		}
	}
	byLen := func(a, b string) int { return cmp.Or(cmp.Compare(len(a), len(b)), cmp.Compare(a, b)) }
	slices.SortFunc(prefix, byLen)
	slices.SortFunc(inside, byLen)
	out := append(prefix, inside...)
	return out[:min(len(out), limit)]
}
